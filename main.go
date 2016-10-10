package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"text/tabwriter"
	"time"
)

func main() {
	// Initial prototype of using serf, to start.

	flag.Parse()
	args := flag.Args()
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "Usage: openvpn-peer [config-file]\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "All settings may also be set via environment variables.\n\n")
		os.Exit(2)
	}
	var config *Config
	var err error
	if len(args) == 1 {
		config, err = ConfigFromFile(args[0])
	} else {
		config, err = ConfigFromEnv()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(2)
	}

	log.Printf("Config is %#v", config)

	gossip := NewGossip(config)

	changeCh := make(chan *ClusterState)
	go gossip.Start(changeCh)

	// Wait for initial state so we know that Serf is ready to join
	clusterState := <-changeCh

	joined, err := gossip.Join(config.InitialPeers)
	if err != nil {
		log.Printf("Initial join failed: %s", err)
	} else {
		log.Printf("Joined a cluster by contacting %d nodes", joined)
	}

	// For now we'll re-evaluate things every 10 seconds.
	// This is far too often for a production system, but is useful at
	// this early stage while we're debugging. In practice probably
	// something more like 15 minutes would make sense, just to ensure
	// we detect any configuration drift somewhat close to its cause.
	//
	// This ticking also gives us an opportunity to re-evaluate our
	// closest nodes as Serf gets updated data about node round-trip times.
	tick := time.Tick(10 * time.Second)

	for {
		// There are actually several different things we're managing
		// here:
		//
		// - The set of all known remote endpoints from Serf becomes our
		//   set of Consul tunnel services. Their health is determined by
		//   the Serf health status, whether we have an OpenVPN process
		//   running at all, and whether the OpenVPN process is connected:
		//       - If OpenVPN isn't running at all or if it's in the
		//         "VPNRetrying" state then the service is Critical.
		//       - If OpenVPN is running and it's in any state other than
		//         "VPNConnected" or "VPNRetrying" then the service is Warning.
		//       - If OpenVPN is running and its state is "VPNConnected"
		//         then the service is passing.
		//
		// - The set of all *live* remote endpoints from Serf becomes our
		//   *target* set of OpenVPN processes. We don't bother to run
		//   OpenVPN processes for dead peers.
		//
		// - The set of all known remote endpoints from Serf is *also* used
		//   to produce the set of destination networks to include in the
		//   route table. The next-hop of each route entry depends on
		//   the OpenVPN status:
		//       - If Serf shows the remote has not alive then the next-hop
		//         is always blackhole, because the remote endpoint is
		//         assumed to be down for everyone (due to Serf Lifeguard).
		//
		//       - If OpenVPN isn't running at all or it isn't in state
		//         VPNConnected then our next-hop gateway is the local IP
		//         address of the nearest endpoint in the local region, which
		//         is presumed to be usable as a fallback.
		//         (If two neighboring endpoints both have the same tunnel
		//         down, they will likely create a route cycle between
		//         each other. The impact of this can be reduced by using
		//         short TTLs on packets to local destinations.)
		//
		//       - If OpenVPN is in state VPNConnected then our next-hop
		//         gateway is the *tunnel* IP address of the remote endpoint.
		//
		//       - If OpenVPN isn't running and there are no other endpoints
		//         in the local region then the next-hop is blackhole.

		PrintState(clusterState)

		remoteEndpoints := make(EndpointSet, len(clusterState.RemoteEndpoints))
		liveRemoteEndpoints := make(EndpointSet, len(remoteEndpoints))
		for _, endpoint := range clusterState.RemoteEndpoints {
			id := endpoint.EndpointId()
			if endpoint.ExpectedAlive() {
				remoteEndpoints.Add(id)
			}
			if endpoint.Alive() {
				liveRemoteEndpoints.Add(id)
			}
		}

		// A Consul service is registered for each remote endpoints that
		// hasn't gracefully left the cluster, including ones that
		// appear to have failed.
		gotServices := make(EndpointSet)
		addServices := remoteEndpoints.Union(gotServices).Subtract(gotServices)
		delServices := remoteEndpoints.Union(gotServices).Subtract(remoteEndpoints)

		// We only create tunnels for remote endpoints that Serf believes
		// to be alive, since if Serf isn't working we expect that OpenVPN
		// won't work either.
		gotTunnels := make(EndpointSet)
		addTunnels := liveRemoteEndpoints.Union(gotTunnels).Subtract(gotTunnels)
		delTunnels := liveRemoteEndpoints.Union(gotTunnels).Subtract(liveRemoteEndpoints)

		log.Printf("All remote endpoints: %#v", remoteEndpoints)
		log.Printf("All live remote endpoints: %#v", liveRemoteEndpoints)
		log.Printf("Add Consul services for %#v", addServices)
		log.Printf("Remove Consul services for %#v", delServices)
		log.Printf("Add tunnels for %#v", addTunnels)
		log.Printf("Remove tunnels for %#v", delTunnels)

		// Now block here until the situation changes somehow.
		// Both the Serf cluster and the OpenVPN tunnel statuses can change;
		// either will cause us to re-evaluate our whole configuration and
		// make changes to "repair" any inconsistencies between expected
		// and actual states.
		select {
		case clusterState = <-changeCh:

		case <-tick:
			// Re-evaluate periodically even if nothing seems to change;
			// this will detect "drift" that we don't get immediate
			// notification about, such as changes to the round-trip times
			// between nodes that may cause us to re-evaluate our choices
			// of nearest neighbors.
		}

	}
}

func PrintState(state *ClusterState) {
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 2, ' ', 0)
	w.Write([]byte("\nname\teid\tglobal address\tlocal address\tregion\tdatacenter\tdistance\tstatus\t\n"))

	printEndpoint := func(e *Endpoint) {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s\t%s:%d\t%s\t%s\t%s\t%d\t%s\t\n",
			e.NodeName(),
			e.EndpointId(),
			e.GossipAddr(),
			e.GossipPort(),
			e.InternalAddr(),
			e.RegionId(),
			e.DatacenterId(),
			e.DistanceTo(state.ThisEndpoint),
			e.Status(),
		)))
	}

	printEndpoint(state.ThisEndpoint)
	for _, endpoint := range state.LocalEndpoints {
		printEndpoint(endpoint)
	}
	for _, endpoint := range state.RemoteEndpoints {
		printEndpoint(endpoint)
	}

	w.Flush()
	os.Stdout.Write([]byte{'\n'})
}

func othermain() {
	remoteAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:1195")
	if err != nil {
		panic(err)
	}
	localAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:1194")
	if err != nil {
		panic(err)
	}
	tunnelRemoteAddr := net.ParseIP("10.8.0.2")
	tunnelLocalAddr := net.ParseIP("10.8.0.1")

	openVPN, err := StartOpenVPN(&VPNConfig{
		OpenVPNPath:  "/usr/sbin/openvpn",
		LauncherPath: "/usr/bin/sudo",

		RemoteAddr: remoteAddr,
		LocalAddr:  localAddr,

		SecretFilename: "/home/mart/Devel/defgrid/openvpn-peer/scratch.key",

		TunnelRemoteAddr: tunnelRemoteAddr,
		TunnelLocalAddr:  tunnelLocalAddr,
	})
	if err != nil {
		panic(err)
	}

	// Shut down OpenVPN after a little while, because we lack
	// any other way to explicitly shut it down right now.
	/*go func () {
		time.Sleep(30 * time.Second)
		err := openVPN.Close()
		if err != nil {
			panic(err)
		}
	}()*/

	var state VPNState
	for state != VPNExited {
		state = openVPN.AwaitStateChange()
		log.Printf("VPN state is now %d", state)
	}
}
