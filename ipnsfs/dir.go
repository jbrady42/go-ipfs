package ipnsfs

import (
	"errors"
	"fmt"
	"os"
	"sync"

	dag "github.com/jbenet/go-ipfs/merkledag"
	ft "github.com/jbenet/go-ipfs/unixfs"
	ufspb "github.com/jbenet/go-ipfs/unixfs/pb"
)

var ErrNotYetImplemented = errors.New("not yet implemented")
var ErrInvalidChild = errors.New("invalid child node")

type Directory struct {
	fs     *Filesystem
	parent childCloser

	childDirs map[string]*Directory
	files     map[string]*File

	lock sync.Mutex
	node *dag.Node

	name string
}

func NewDirectory(name string, node *dag.Node, parent childCloser, fs *Filesystem) *Directory {
	return &Directory{
		fs:        fs,
		name:      name,
		node:      node,
		parent:    parent,
		childDirs: make(map[string]*Directory),
		files:     make(map[string]*File),
	}
}

// Open opens a file at the given path 'tpath'
func (d *Directory) Open(tpath []string, mode int) (*File, error) {
	if len(tpath) == 0 {
		return nil, ErrIsDirectory
	}
	if len(tpath) == 1 {
		fi, err := d.childFile(tpath[0])
		if err == nil {
			return fi, nil
		}

		if mode|os.O_CREATE != 0 {
			fnode := new(dag.Node)
			fnode.Data = ft.FilePBData(nil, 0)
			nfi, err := NewFile(tpath[0], fnode, d, d.fs)
			if err != nil {
				return nil, err
			}
			d.files[tpath[0]] = nfi
			return nfi, nil
		}

		return nil, ErrNoSuch
	}

	dir, err := d.childDir(tpath[0])
	if err != nil {
		return nil, err
	}
	return dir.Open(tpath[1:], mode)
}

type childCloser interface {
	closeChild(string, *dag.Node) error
}

func (d *Directory) closeChild(name string, nd *dag.Node) error {
	_, err := d.fs.dserv.Add(nd)
	if err != nil {
		return err
	}

	d.lock.Lock()
	defer d.lock.Unlock()
	err = d.node.RemoveNodeLink(name)
	if err != nil && err != dag.ErrNotFound {
		return err
	}

	err = d.node.AddNodeLinkClean(name, nd)
	if err != nil {
		return err
	}

	return d.parent.closeChild(d.name, d.node)
}

func (d *Directory) Type() NodeType {
	return TDir
}

func (d *Directory) childFile(name string) (*File, error) {
	fi, ok := d.files[name]
	if ok {
		return fi, nil
	}

	// search dag
	for _, lnk := range d.node.Links {
		if lnk.Name == name {
			nd, err := lnk.GetNode(d.fs.dserv)
			if err != nil {
				return nil, err
			}
			i, err := ft.FromBytes(nd.Data)
			if err != nil {
				return nil, err
			}

			switch i.GetType() {
			case ufspb.Data_Directory:
				return nil, ErrIsDirectory
			case ufspb.Data_File:
				nfi, err := NewFile(name, nd, d, d.fs)
				if err != nil {
					return nil, err
				}
				d.files[name] = nfi
				return nfi, nil
			case ufspb.Data_Metadata:
				return nil, ErrNotYetImplemented
			default:
				return nil, ErrInvalidChild
			}
		}
	}
	return nil, ErrNoSuch
}

func (d *Directory) childDir(name string) (*Directory, error) {
	dir, ok := d.childDirs[name]
	if ok {
		return dir, nil
	}

	for _, lnk := range d.node.Links {
		if lnk.Name == name {
			nd, err := lnk.GetNode(d.fs.dserv)
			if err != nil {
				return nil, err
			}
			i, err := ft.FromBytes(nd.Data)
			if err != nil {
				return nil, err
			}

			switch i.GetType() {
			case ufspb.Data_Directory:
				ndir := NewDirectory(name, nd, d, d.fs)
				d.childDirs[name] = ndir
				return ndir, nil
			case ufspb.Data_File:
				return nil, fmt.Errorf("%s is not a directory", name)
			case ufspb.Data_Metadata:
				return nil, ErrNotYetImplemented
			default:
				return nil, ErrInvalidChild
			}
		}

	}

	return nil, ErrNoSuch
}

func (d *Directory) Child(name string) (FSNode, error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.childUnsync(name)
}

func (d *Directory) childUnsync(name string) (FSNode, error) {
	dir, err := d.childDir(name)
	if err == nil {
		return dir, nil
	}
	fi, err := d.childFile(name)
	if err == nil {
		return fi, nil
	}

	return nil, ErrNoSuch
}

func (d *Directory) List() []string {
	d.lock.Lock()
	defer d.lock.Unlock()

	var out []string
	for _, lnk := range d.node.Links {
		out = append(out, lnk.Name)
	}
	return out
}

func (d *Directory) Mkdir(name string) (*Directory, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	_, err := d.childDir(name)
	if err == nil {
		return nil, os.ErrExist
	}
	_, err = d.childFile(name)
	if err == nil {
		return nil, os.ErrExist
	}

	ndir := &dag.Node{Data: ft.FolderPBData()}
	err = d.node.AddNodeLinkClean(name, ndir)
	if err != nil {
		return nil, err
	}

	err = d.parent.closeChild(d.name, d.node)
	if err != nil {
		return nil, err
	}

	return d.childDir(name)
}

func (d *Directory) Unlink(name string) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	delete(d.childDirs, name)
	delete(d.files, name)

	err := d.node.RemoveNodeLink(name)
	if err != nil {
		return err
	}

	return d.parent.closeChild(d.name, d.node)
}

// RenameEntry renames the child by 'oldname' of this directory to 'newname'
func (d *Directory) RenameEntry(oldname, newname string) error {
	d.Lock()
	defer d.Unlock()
	// Is the child a directory?
	dir, err := d.childDir(oldname)
	if err == nil {
		dir.name = newname

		err := d.node.RemoveNodeLink(oldname)
		if err != nil {
			return err
		}
		err = d.node.AddNodeLinkClean(newname, dir.node)
		if err != nil {
			return err
		}

		delete(d.childDirs, oldname)
		d.childDirs[newname] = dir
		return d.parent.closeChild(d.name, d.node)
	}

	// Is the child a file?
	fi, err := d.childFile(oldname)
	if err == nil {
		fi.name = newname

		err := d.node.RemoveNodeLink(oldname)
		if err != nil {
			return err
		}

		nd, err := fi.GetNode()
		if err != nil {
			return err
		}

		err = d.node.AddNodeLinkClean(newname, nd)
		if err != nil {
			return err
		}

		delete(d.childDirs, oldname)
		d.files[newname] = fi
		return d.parent.closeChild(d.name, d.node)
	}
	return ErrNoSuch
}

// AddChild adds the node 'nd' under this directory giving it the name 'name'
func (d *Directory) AddChild(name string, nd *dag.Node) error {
	d.Lock()
	defer d.Unlock()
	pbn, err := ft.FromBytes(nd.Data)
	if err != nil {
		return err
	}

	_, err = d.childUnsync(name)
	if err == nil {
		return errors.New("directory already has entry by that name")
	}

	err = d.node.AddNodeLinkClean(name, nd)
	if err != nil {
		return err
	}

	switch pbn.GetType() {
	case ft.TDirectory:
		d.childDirs[name] = NewDirectory(name, nd, d, d.fs)
	case ft.TFile, ft.TMetadata, ft.TRaw:
		nfi, err := NewFile(name, nd, d, d.fs)
		if err != nil {
			return err
		}
		d.files[name] = nfi
	default:
		return ErrInvalidChild
	}
	return d.parent.closeChild(d.name, d.node)
}

func (d *Directory) GetNode() (*dag.Node, error) {
	return d.node, nil
}

func (d *Directory) Lock() {
	d.lock.Lock()
}

func (d *Directory) Unlock() {
	d.lock.Unlock()
}