package dht

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	ds "github.com/ipfs/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-datastore"
	dssync "github.com/ipfs/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-datastore/sync"
	ma "github.com/ipfs/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multiaddr"
	context "github.com/ipfs/go-ipfs/Godeps/_workspace/src/golang.org/x/net/context"

	peer "github.com/ipfs/go-ipfs/p2p/peer"
	netutil "github.com/ipfs/go-ipfs/p2p/test/util"
	routing "github.com/ipfs/go-ipfs/routing"
	record "github.com/ipfs/go-ipfs/routing/record"
	u "github.com/ipfs/go-ipfs/util"

	ci "github.com/ipfs/go-ipfs/util/testutil/ci"
	travisci "github.com/ipfs/go-ipfs/util/testutil/ci/travis"
)

var testCaseValues = map[u.Key][]byte{}

func init() {
	testCaseValues["hello"] = []byte("world")
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("%d -- key", i)
		v := fmt.Sprintf("%d -- value", i)
		testCaseValues[u.Key(k)] = []byte(v)
	}
}

func setupDHT(ctx context.Context, t *testing.T) *IpfsDHT {
	h := netutil.GenHostSwarm(t, ctx)

	dss := dssync.MutexWrap(ds.NewMapDatastore())
	d := NewDHT(ctx, h, dss)

	d.Validator["v"] = &record.ValidChecker{
		Func: func(u.Key, []byte) error {
			return nil
		},
		Sign: false,
	}
	return d
}

func setupDHTS(ctx context.Context, n int, t *testing.T) ([]ma.Multiaddr, []peer.ID, []*IpfsDHT) {
	addrs := make([]ma.Multiaddr, n)
	dhts := make([]*IpfsDHT, n)
	peers := make([]peer.ID, n)

	for i := 0; i < n; i++ {
		dhts[i] = setupDHT(ctx, t)
		peers[i] = dhts[i].self
		addrs[i] = dhts[i].peerstore.Addrs(dhts[i].self)[0]
	}

	return addrs, peers, dhts
}

func connect(t *testing.T, ctx context.Context, a, b *IpfsDHT) {

	idB := b.self
	addrB := b.peerstore.Addrs(idB)
	if len(addrB) == 0 {
		t.Fatal("peers setup incorrectly: no local address")
	}

	a.peerstore.AddAddrs(idB, addrB, peer.TempAddrTTL)
	if err := a.Connect(ctx, idB); err != nil {
		t.Fatal(err)
	}
}

func connectDHTS(t *testing.T, ctx context.Context, dhts []*IpfsDHT, start, end, target int) []peer.ID {
	var ret []peer.ID
	for a := start; a < end; a++ {
		connect(t, ctx, dhts[a], dhts[target])
		ret = append(ret, dhts[a].self)
	}
	return ret
}

func bootstrap(t *testing.T, ctx context.Context, dhts []*IpfsDHT) {

	ctx, cancel := context.WithCancel(ctx)
	log.Debugf("bootstrapping dhts...")

	// tried async. sequential fares much better. compare:
	// 100 async https://gist.github.com/jbenet/56d12f0578d5f34810b2
	// 100 sync https://gist.github.com/jbenet/6c59e7c15426e48aaedd
	// probably because results compound

	var cfg BootstrapConfig
	cfg = DefaultBootstrapConfig
	cfg.Queries = 3

	start := rand.Intn(len(dhts)) // randomize to decrease bias.
	for i := range dhts {
		dht := dhts[(start+i)%len(dhts)]
		dht.runBootstrap(ctx, cfg)
	}
	cancel()
}

func TestPing(t *testing.T) {
	// t.Skip("skipping test to debug another")
	ctx := context.Background()

	dhtA := setupDHT(ctx, t)
	dhtB := setupDHT(ctx, t)

	peerA := dhtA.self
	peerB := dhtB.self

	defer dhtA.Close()
	defer dhtB.Close()
	defer dhtA.host.Close()
	defer dhtB.host.Close()

	connect(t, ctx, dhtA, dhtB)

	//Test that we can ping the node
	ctxT, _ := context.WithTimeout(ctx, 100*time.Millisecond)
	if _, err := dhtA.Ping(ctxT, peerB); err != nil {
		t.Fatal(err)
	}

	ctxT, _ = context.WithTimeout(ctx, 100*time.Millisecond)
	if _, err := dhtB.Ping(ctxT, peerA); err != nil {
		t.Fatal(err)
	}
}

func TestValueGetSet(t *testing.T) {
	// t.Skip("skipping test to debug another")

	ctx := context.Background()

	dhtA := setupDHT(ctx, t)
	dhtB := setupDHT(ctx, t)

	defer dhtA.Close()
	defer dhtB.Close()
	defer dhtA.host.Close()
	defer dhtB.host.Close()

	vf := &record.ValidChecker{
		Func: func(u.Key, []byte) error {
			return nil
		},
		Sign: false,
	}
	dhtA.Validator["v"] = vf
	dhtB.Validator["v"] = vf

	connect(t, ctx, dhtA, dhtB)

	ctxT, _ := context.WithTimeout(ctx, time.Second)
	dhtA.PutValue(ctxT, "/v/hello", []byte("world"))

	ctxT, _ = context.WithTimeout(ctx, time.Second*2)
	val, err := dhtA.GetValue(ctxT, "/v/hello")
	if err != nil {
		t.Fatal(err)
	}

	if string(val) != "world" {
		t.Fatalf("Expected 'world' got '%s'", string(val))
	}

	ctxT, _ = context.WithTimeout(ctx, time.Second*2)
	val, err = dhtB.GetValue(ctxT, "/v/hello")
	if err != nil {
		t.Fatal(err)
	}

	if string(val) != "world" {
		t.Fatalf("Expected 'world' got '%s'", string(val))
	}
}

func TestProvides(t *testing.T) {
	// t.Skip("skipping test to debug another")
	ctx := context.Background()

	_, _, dhts := setupDHTS(ctx, 4, t)
	defer func() {
		for i := 0; i < 4; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	connect(t, ctx, dhts[0], dhts[1])
	connect(t, ctx, dhts[1], dhts[2])
	connect(t, ctx, dhts[1], dhts[3])

	for k, v := range testCaseValues {
		log.Debugf("adding local values for %s = %s", k, v)
		sk := dhts[3].peerstore.PrivKey(dhts[3].self)
		rec, err := record.MakePutRecord(sk, k, v, false)
		if err != nil {
			t.Fatal(err)
		}

		err = dhts[3].putLocal(k, rec)
		if err != nil {
			t.Fatal(err)
		}

		bits, err := dhts[3].getLocal(k)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(bits, v) {
			t.Fatal("didn't store the right bits (%s, %s)", k, v)
		}
	}

	for k, _ := range testCaseValues {
		log.Debugf("announcing provider for %s", k)
		if err := dhts[3].Provide(ctx, k); err != nil {
			t.Fatal(err)
		}
	}

	// what is this timeout for? was 60ms before.
	time.Sleep(time.Millisecond * 6)

	n := 0
	for k, _ := range testCaseValues {
		n = (n + 1) % 3

		log.Debugf("getting providers for %s from %d", k, n)
		ctxT, _ := context.WithTimeout(ctx, time.Second)
		provchan := dhts[n].FindProvidersAsync(ctxT, k, 1)

		select {
		case prov := <-provchan:
			if prov.ID == "" {
				t.Fatal("Got back nil provider")
			}
			if prov.ID != dhts[3].self {
				t.Fatal("Got back wrong provider")
			}
		case <-ctxT.Done():
			t.Fatal("Did not get a provider back.")
		}
	}
}

// if minPeers or avgPeers is 0, dont test for it.
func waitForWellFormedTables(t *testing.T, dhts []*IpfsDHT, minPeers, avgPeers int, timeout time.Duration) bool {
	// test "well-formed-ness" (>= minPeers peers in every routing table)

	checkTables := func() bool {
		totalPeers := 0
		for _, dht := range dhts {
			rtlen := dht.routingTable.Size()
			totalPeers += rtlen
			if minPeers > 0 && rtlen < minPeers {
				t.Logf("routing table for %s only has %d peers (should have >%d)", dht.self, rtlen, minPeers)
				return false
			}
		}
		actualAvgPeers := totalPeers / len(dhts)
		t.Logf("avg rt size: %d", actualAvgPeers)
		if avgPeers > 0 && actualAvgPeers < avgPeers {
			t.Logf("avg rt size: %d < %d", actualAvgPeers, avgPeers)
			return false
		}
		return true
	}

	timeoutA := time.After(timeout)
	for {
		select {
		case <-timeoutA:
			log.Debugf("did not reach well-formed routing tables by %s", timeout)
			return false // failed
		case <-time.After(5 * time.Millisecond):
			if checkTables() {
				return true // succeeded
			}
		}
	}
}

func printRoutingTables(dhts []*IpfsDHT) {
	// the routing tables should be full now. let's inspect them.
	fmt.Println("checking routing table of %d", len(dhts))
	for _, dht := range dhts {
		fmt.Printf("checking routing table of %s\n", dht.self)
		dht.routingTable.Print()
		fmt.Println("")
	}
}

func TestBootstrap(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()

	nDHTs := 30
	_, _, dhts := setupDHTS(ctx, nDHTs, t)
	defer func() {
		for i := 0; i < nDHTs; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	t.Logf("connecting %d dhts in a ring", nDHTs)
	for i := 0; i < nDHTs; i++ {
		connect(t, ctx, dhts[i], dhts[(i+1)%len(dhts)])
	}

	<-time.After(100 * time.Millisecond)
	// bootstrap a few times until we get good tables.
	stop := make(chan struct{})
	go func() {
		for {
			t.Logf("bootstrapping them so they find each other", nDHTs)
			ctxT, _ := context.WithTimeout(ctx, 5*time.Second)
			bootstrap(t, ctxT, dhts)

			select {
			case <-time.After(50 * time.Millisecond):
				continue // being explicit
			case <-stop:
				return
			}
		}
	}()

	waitForWellFormedTables(t, dhts, 7, 10, 20*time.Second)
	close(stop)

	if u.Debug {
		// the routing tables should be full now. let's inspect them.
		printRoutingTables(dhts)
	}
}

func TestPeriodicBootstrap(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if ci.IsRunning() {
		t.Skip("skipping on CI. highly timing dependent")
	}
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()

	nDHTs := 30
	_, _, dhts := setupDHTS(ctx, nDHTs, t)
	defer func() {
		for i := 0; i < nDHTs; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	// signal amplifier
	amplify := func(signal chan time.Time, other []chan time.Time) {
		for t := range signal {
			for _, s := range other {
				s <- t
			}
		}
		for _, s := range other {
			close(s)
		}
	}

	signal := make(chan time.Time)
	allSignals := []chan time.Time{}

	var cfg BootstrapConfig
	cfg = DefaultBootstrapConfig
	cfg.Queries = 5

	// kick off periodic bootstrappers with instrumented signals.
	for _, dht := range dhts {
		s := make(chan time.Time)
		allSignals = append(allSignals, s)
		dht.BootstrapOnSignal(cfg, s)
	}
	go amplify(signal, allSignals)

	t.Logf("dhts are not connected.", nDHTs)
	for _, dht := range dhts {
		rtlen := dht.routingTable.Size()
		if rtlen > 0 {
			t.Errorf("routing table for %s should have 0 peers. has %d", dht.self, rtlen)
		}
	}

	for i := 0; i < nDHTs; i++ {
		connect(t, ctx, dhts[i], dhts[(i+1)%len(dhts)])
	}

	t.Logf("dhts are now connected to 1-2 others.", nDHTs)
	for _, dht := range dhts {
		rtlen := dht.routingTable.Size()
		if rtlen > 2 {
			t.Errorf("routing table for %s should have at most 2 peers. has %d", dht.self, rtlen)
		}
	}

	if u.Debug {
		printRoutingTables(dhts)
	}

	t.Logf("bootstrapping them so they find each other", nDHTs)
	signal <- time.Now()

	// this is async, and we dont know when it's finished with one cycle, so keep checking
	// until the routing tables look better, or some long timeout for the failure case.
	waitForWellFormedTables(t, dhts, 7, 10, 20*time.Second)

	if u.Debug {
		printRoutingTables(dhts)
	}
}

func TestProvidesMany(t *testing.T) {
	t.Skip("this test doesn't work")
	// t.Skip("skipping test to debug another")
	ctx := context.Background()

	nDHTs := 40
	_, _, dhts := setupDHTS(ctx, nDHTs, t)
	defer func() {
		for i := 0; i < nDHTs; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	t.Logf("connecting %d dhts in a ring", nDHTs)
	for i := 0; i < nDHTs; i++ {
		connect(t, ctx, dhts[i], dhts[(i+1)%len(dhts)])
	}

	<-time.After(100 * time.Millisecond)
	t.Logf("bootstrapping them so they find each other", nDHTs)
	ctxT, _ := context.WithTimeout(ctx, 20*time.Second)
	bootstrap(t, ctxT, dhts)

	if u.Debug {
		// the routing tables should be full now. let's inspect them.
		t.Logf("checking routing table of %d", nDHTs)
		for _, dht := range dhts {
			fmt.Printf("checking routing table of %s\n", dht.self)
			dht.routingTable.Print()
			fmt.Println("")
		}
	}

	var providers = map[u.Key]peer.ID{}

	d := 0
	for k, v := range testCaseValues {
		d = (d + 1) % len(dhts)
		dht := dhts[d]
		providers[k] = dht.self

		t.Logf("adding local values for %s = %s (on %s)", k, v, dht.self)
		rec, err := record.MakePutRecord(nil, k, v, false)
		if err != nil {
			t.Fatal(err)
		}

		err = dht.putLocal(k, rec)
		if err != nil {
			t.Fatal(err)
		}

		bits, err := dht.getLocal(k)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(bits, v) {
			t.Fatal("didn't store the right bits (%s, %s)", k, v)
		}

		t.Logf("announcing provider for %s", k)
		if err := dht.Provide(ctx, k); err != nil {
			t.Fatal(err)
		}
	}

	// what is this timeout for? was 60ms before.
	time.Sleep(time.Millisecond * 6)

	errchan := make(chan error)

	ctxT, _ = context.WithTimeout(ctx, 5*time.Second)

	var wg sync.WaitGroup
	getProvider := func(dht *IpfsDHT, k u.Key) {
		defer wg.Done()

		expected := providers[k]

		provchan := dht.FindProvidersAsync(ctxT, k, 1)
		select {
		case prov := <-provchan:
			actual := prov.ID
			if actual == "" {
				errchan <- fmt.Errorf("Got back nil provider (%s at %s)", k, dht.self)
			} else if actual != expected {
				errchan <- fmt.Errorf("Got back wrong provider (%s != %s) (%s at %s)",
					expected, actual, k, dht.self)
			}
		case <-ctxT.Done():
			errchan <- fmt.Errorf("Did not get a provider back (%s at %s)", k, dht.self)
		}
	}

	for k, _ := range testCaseValues {
		// everyone should be able to find it...
		for _, dht := range dhts {
			log.Debugf("getting providers for %s at %s", k, dht.self)
			wg.Add(1)
			go getProvider(dht, k)
		}
	}

	// we need this because of printing errors
	go func() {
		wg.Wait()
		close(errchan)
	}()

	for err := range errchan {
		t.Error(err)
	}
}

func TestProvidesAsync(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()

	_, _, dhts := setupDHTS(ctx, 4, t)
	defer func() {
		for i := 0; i < 4; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	connect(t, ctx, dhts[0], dhts[1])
	connect(t, ctx, dhts[1], dhts[2])
	connect(t, ctx, dhts[1], dhts[3])

	k := u.Key("hello")
	val := []byte("world")
	sk := dhts[3].peerstore.PrivKey(dhts[3].self)
	rec, err := record.MakePutRecord(sk, k, val, false)
	if err != nil {
		t.Fatal(err)
	}

	err = dhts[3].putLocal(k, rec)
	if err != nil {
		t.Fatal(err)
	}

	bits, err := dhts[3].getLocal(k)
	if err != nil && bytes.Equal(bits, val) {
		t.Fatal(err)
	}

	err = dhts[3].Provide(ctx, u.Key("hello"))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond * 60)

	ctxT, _ := context.WithTimeout(ctx, time.Millisecond*300)
	provs := dhts[0].FindProvidersAsync(ctxT, u.Key("hello"), 5)
	select {
	case p, ok := <-provs:
		if !ok {
			t.Fatal("Provider channel was closed...")
		}
		if p.ID == "" {
			t.Fatal("Got back nil provider!")
		}
		if p.ID != dhts[3].self {
			t.Fatalf("got a provider, but not the right one. %s", p)
		}
	case <-ctxT.Done():
		t.Fatal("Didnt get back providers")
	}
}

func TestLayeredGet(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()

	_, _, dhts := setupDHTS(ctx, 4, t)
	defer func() {
		for i := 0; i < 4; i++ {
			dhts[i].Close()
			defer dhts[i].host.Close()
		}
	}()

	connect(t, ctx, dhts[0], dhts[1])
	connect(t, ctx, dhts[1], dhts[2])
	connect(t, ctx, dhts[1], dhts[3])

	err := dhts[3].Provide(ctx, u.Key("/v/hello"))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond * 6)

	t.Log("interface was changed. GetValue should not use providers.")
	ctxT, _ := context.WithTimeout(ctx, time.Second)
	val, err := dhts[0].GetValue(ctxT, u.Key("/v/hello"))
	if err != routing.ErrNotFound {
		t.Error(err)
	}
	if string(val) == "world" {
		t.Error("should not get value.")
	}
	if len(val) > 0 && string(val) != "world" {
		t.Error("worse, there's a value and its not even the right one.")
	}
}

func TestFindPeer(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()

	_, peers, dhts := setupDHTS(ctx, 4, t)
	defer func() {
		for i := 0; i < 4; i++ {
			dhts[i].Close()
			dhts[i].host.Close()
		}
	}()

	connect(t, ctx, dhts[0], dhts[1])
	connect(t, ctx, dhts[1], dhts[2])
	connect(t, ctx, dhts[1], dhts[3])

	ctxT, _ := context.WithTimeout(ctx, time.Second)
	p, err := dhts[0].FindPeer(ctxT, peers[2])
	if err != nil {
		t.Fatal(err)
	}

	if p.ID == "" {
		t.Fatal("Failed to find peer.")
	}

	if p.ID != peers[2] {
		t.Fatal("Didnt find expected peer.")
	}
}

func TestFindPeersConnectedToPeer(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	ctx := context.Background()
	dhtCount := 50
	_, peers, dhts := setupDHTS(ctx, dhtCount, t)
	defer func() {
		for i := 0; i < dhtCount; i++ {
			dhts[i].Close()
			dhts[i].host.Close()
		}
	}()

	// Topology
	// 0-1, [2:49]-1
	srcInd := 0
	tgtInd := 1
	shouldFind := connectDHTS(t, ctx, dhts, 2, 49, tgtInd)
	connect(t, ctx, dhts[srcInd], dhts[tgtInd])

	ctxT, _ := context.WithTimeout(ctx, time.Second)
	pchan, err := dhts[srcInd].FindPeersConnectedToPeer(ctxT, peers[tgtInd])
	if err != nil {
		t.Fatal(err)
	}

	testSame := func(id peer.ID) {
		if id == peers[srcInd] {
			t.Errorf("should not contain id of requesting peer %s", id)
		}
		if id == peers[tgtInd] {
			t.Errorf("should not contain id of target peer %s", id)
		}
	}

	found := []peer.ID{}
	for nextp := range pchan {
		id := nextp.ID
		found = append(found, id)
		testSame(id)
	}

	if len(found) == 0 {
		t.Fatal("didn't find any peers.")
	}

	matchErr := testPeerListsMatch(t, shouldFind, found)
	if matchErr {
		fmt.Println("Should find: ", shouldFind)
		fmt.Println("Found: ", found)
	}
}

func testPeerListsMatch(t *testing.T, p1, p2 []peer.ID) bool {
	err := false
	if len(p1) != len(p2) {
		t.Errorf("size mismatch. expected: %d found: %d", len(p1), len(p2))
		err = true
	}

	ids2 := make(map[string]interface{})
	for _, p := range p2 {
		ids2[p.String()] = struct{}{}
	}

	for _, id := range p1 {
		if _, ok := ids2[id.String()]; !ok {
			t.Errorf("did not find expected peer %s\n", id)
			err = true
		}
	}
	return err
}

func TestConnectCollision(t *testing.T) {
	// t.Skip("skipping test to debug another")
	if testing.Short() {
		t.SkipNow()
	}
	if travisci.IsRunning() {
		t.Skip("Skipping on Travis-CI.")
	}

	runTimes := 10

	for rtime := 0; rtime < runTimes; rtime++ {
		log.Notice("Running Time: ", rtime)

		ctx := context.Background()

		dhtA := setupDHT(ctx, t)
		dhtB := setupDHT(ctx, t)

		addrA := dhtA.peerstore.Addrs(dhtA.self)[0]
		addrB := dhtB.peerstore.Addrs(dhtB.self)[0]

		peerA := dhtA.self
		peerB := dhtB.self

		errs := make(chan error)
		go func() {
			dhtA.peerstore.AddAddr(peerB, addrB, peer.TempAddrTTL)
			err := dhtA.Connect(ctx, peerB)
			errs <- err
		}()
		go func() {
			dhtB.peerstore.AddAddr(peerA, addrA, peer.TempAddrTTL)
			err := dhtB.Connect(ctx, peerA)
			errs <- err
		}()

		timeout := time.After(5 * time.Second)
		select {
		case e := <-errs:
			if e != nil {
				t.Fatal(e)
			}
		case <-timeout:
			t.Fatal("Timeout received!")
		}
		select {
		case e := <-errs:
			if e != nil {
				t.Fatal(e)
			}
		case <-timeout:
			t.Fatal("Timeout received!")
		}

		dhtA.Close()
		dhtB.Close()
		dhtA.host.Close()
		dhtB.host.Close()
	}
}
