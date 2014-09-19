package dht

import (
	"testing"

	context "github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"

	ds "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/datastore.go"
	ma "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multiaddr"

	ci "github.com/jbenet/go-ipfs/crypto"
	spipe "github.com/jbenet/go-ipfs/crypto/spipe"
	inet "github.com/jbenet/go-ipfs/net"
	mux "github.com/jbenet/go-ipfs/net/mux"
	netservice "github.com/jbenet/go-ipfs/net/service"
	peer "github.com/jbenet/go-ipfs/peer"
	u "github.com/jbenet/go-ipfs/util"

	"fmt"
	"time"
)

func setupDHT(t *testing.T, p *peer.Peer) *IpfsDHT {
	ctx, _ := context.WithCancel(context.TODO())

	peerstore := peer.NewPeerstore()

	dhts := netservice.NewService(nil) // nil handler for now, need to patch it
	if err := dhts.Start(ctx); err != nil {
		t.Fatal(err)
	}

	net, err := inet.NewIpfsNetwork(ctx, p, &mux.ProtocolMap{
		mux.ProtocolID_Routing: dhts,
	})
	if err != nil {
		t.Fatal(err)
	}

	d := NewDHT(p, peerstore, net, dhts, ds.NewMapDatastore())
	dhts.Handler = d
	return d
}

func setupDHTS(n int, t *testing.T) ([]*ma.Multiaddr, []*peer.Peer, []*IpfsDHT) {
	var addrs []*ma.Multiaddr
	for i := 0; i < 4; i++ {
		a, err := ma.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 5000+i))
		if err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, a)
	}

	var peers []*peer.Peer
	for i := 0; i < 4; i++ {
		p := makePeer(addrs[i])
		peers = append(peers, p)
	}

	var dhts []*IpfsDHT
	for i := 0; i < 4; i++ {
		dhts[i] = setupDHT(t, peers[i])
	}

	return addrs, peers, dhts
}

func makePeer(addr *ma.Multiaddr) *peer.Peer {
	p := new(peer.Peer)
	p.AddAddress(addr)
	sk, pk, err := ci.GenerateKeyPair(ci.RSA, 512)
	if err != nil {
		panic(err)
	}
	p.PrivKey = sk
	p.PubKey = pk
	id, err := spipe.IDFromPubKey(pk)
	if err != nil {
		panic(err)
	}

	p.ID = id
	return p
}

func TestPing(t *testing.T) {
	u.Debug = true
	addrA, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/2222")
	if err != nil {
		t.Fatal(err)
	}
	addrB, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/5678")
	if err != nil {
		t.Fatal(err)
	}

	peerA := makePeer(addrA)
	peerB := makePeer(addrB)

	dhtA := setupDHT(t, peerA)
	dhtB := setupDHT(t, peerB)

	defer dhtA.Halt()
	defer dhtB.Halt()

	_, err = dhtA.Connect(peerB)
	if err != nil {
		t.Fatal(err)
	}

	//Test that we can ping the node
	err = dhtA.Ping(peerB, time.Second*2)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValueGetSet(t *testing.T) {
	u.Debug = false
	addrA, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1235")
	if err != nil {
		t.Fatal(err)
	}
	addrB, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/5679")
	if err != nil {
		t.Fatal(err)
	}

	peerA := makePeer(addrA)
	peerB := makePeer(addrB)

	dhtA := setupDHT(t, peerA)
	dhtB := setupDHT(t, peerB)

	defer dhtA.Halt()
	defer dhtB.Halt()

	_, err = dhtA.Connect(peerB)
	if err != nil {
		t.Fatal(err)
	}

	dhtA.PutValue("hello", []byte("world"))

	val, err := dhtA.GetValue("hello", time.Second*2)
	if err != nil {
		t.Fatal(err)
	}

	if string(val) != "world" {
		t.Fatalf("Expected 'world' got '%s'", string(val))
	}

}

// func TestProvides(t *testing.T) {
// 	u.Debug = false
//
// 	_, peers, dhts := setupDHTS(4, t)
// 	defer func() {
// 		for i := 0; i < 4; i++ {
// 			dhts[i].Halt()
// 		}
// 	}()
//
// 	_, err := dhts[0].Connect(peers[1])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	_, err = dhts[1].Connect(peers[2])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	_, err = dhts[1].Connect(peers[3])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	err = dhts[3].putLocal(u.Key("hello"), []byte("world"))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	bits, err := dhts[3].getLocal(u.Key("hello"))
// 	if err != nil && bytes.Equal(bits, []byte("world")) {
// 		t.Fatal(err)
// 	}
//
// 	err = dhts[3].Provide(u.Key("hello"))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	time.Sleep(time.Millisecond * 60)
//
// 	provs, err := dhts[0].FindProviders(u.Key("hello"), time.Second)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	if len(provs) != 1 {
// 		t.Fatal("Didnt get back providers")
// 	}
// }
//
// func TestLayeredGet(t *testing.T) {
// 	u.Debug = false
// 	addrs, _, dhts := setupDHTS(4, t)
// 	defer func() {
// 		for i := 0; i < 4; i++ {
// 			dhts[i].Halt()
// 		}
// 	}()
//
// 	_, err := dhts[0].Connect(addrs[1])
// 	if err != nil {
// 		t.Fatalf("Failed to connect: %s", err)
// 	}
//
// 	_, err = dhts[1].Connect(addrs[2])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	_, err = dhts[1].Connect(addrs[3])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	err = dhts[3].putLocal(u.Key("hello"), []byte("world"))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	err = dhts[3].Provide(u.Key("hello"))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	time.Sleep(time.Millisecond * 60)
//
// 	val, err := dhts[0].GetValue(u.Key("hello"), time.Second)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	if string(val) != "world" {
// 		t.Fatal("Got incorrect value.")
// 	}
//
// }
//
// func TestFindPeer(t *testing.T) {
// 	u.Debug = false
//
// 	addrs, peers, dhts := setupDHTS(4, t)
// 	go func() {
// 		for i := 0; i < 4; i++ {
// 			dhts[i].Halt()
// 		}
// 	}()
//
// 	_, err := dhts[0].Connect(addrs[1])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	_, err = dhts[1].Connect(addrs[2])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	_, err = dhts[1].Connect(addrs[3])
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	p, err := dhts[0].FindPeer(peers[2].ID, time.Second)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	if p == nil {
// 		t.Fatal("Failed to find peer.")
// 	}
//
// 	if !p.ID.Equal(peers[2].ID) {
// 		t.Fatal("Didnt find expected peer.")
// 	}
// }
