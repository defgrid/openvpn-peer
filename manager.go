package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"time"
)

type Manager struct {
	gossip             *Gossip
	initialGossipPeers []string
	secretFilename     string
}

func NewManager(config *Config) (*Manager, error) {

	var err error

	localIP, err := interfaceIPAddr(config.LocalInterface)
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(config.DataDir, os.ModeDir|0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s: %s", config.DataDir, err)
	}

	addressing := &Addressing{
		CommonPrefixLen:      config.CommonPrefixLen,
		RegionPrefixLen:      config.RegionPrefixLen,
		DCPrefixLen:          config.DCPrefixLen,
		LocalIPAddr:          net.ParseIP(localIP),
		VPNEndpointStartPort: config.VPNEndpointStartPort,
	}

	gossip := NewGossip(&GossipConfig{
		NodeName:        config.NodeName,
		ListenIPAddr:    localIP,
		AdvertiseIPAddr: config.PublicIPAddress,
		Port:            config.GossipPort,
		DataDir:         path.Join(config.DataDir, "serf"),
		Addressing:      addressing,
	})

	return &Manager{
		gossip:             gossip,
		initialGossipPeers: config.InitialPeers,
		secretFilename:     config.VPNKeyFilename,
	}, nil
}

// Run begins the process of managing the local tunnel configuration.
//
// This function returns only if there is an error while starting up.
func (m *Manager) Run() {
	clusterStateCh := make(chan *ClusterState)
	go m.gossip.Start(clusterStateCh)

	// Wait for initial state so we know that Serf is ready to join
	clusterState := <-clusterStateCh

	if len(m.initialGossipPeers) != 0 {
		joined, err := m.gossip.Join(m.initialGossipPeers)
		if err != nil {
			log.Printf("Initial join failed: %s", err)
		} else {
			log.Printf("Joined a cluster by contacting %d nodes", joined)
		}
	}

	tunnelStateCh := make(chan *TunnelsState)
	tunnelState := &TunnelsState{
		Tunnels: []*Tunnel{},
	}
	tunnelMgr := NewTunnelMgr(&TunnelMgrConfig{
		SecretFilename: m.secretFilename,
		LocalEndpoint:  clusterState.ThisEndpoint,
	}, tunnelStateCh)

	// For now we'll re-evaluate things every 10 seconds.
	// This is far too often for a production system, but is useful at
	// this early stage while we're debugging. In practice probably
	// something more like 15 minutes would make sense, just to ensure
	// we detect any configuration drift somewhat close to its cause.
	//
	// This ticking also gives us an opportunity to re-evaluate our
	// closest nodes as Serf gets updated data about node round-trip times.
	refreshTime := 10 * time.Second
	timeout := time.NewTimer(refreshTime)

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

		PrintClusterState(clusterState)
		PrintTunnelState(tunnelState)

		endpoints := make(map[EndpointId]*Endpoint)
		remoteEndpoints := make(EndpointSet, len(clusterState.RemoteEndpoints))
		liveRemoteEndpoints := make(EndpointSet, len(remoteEndpoints))
		for _, endpoint := range clusterState.RemoteEndpoints {
			id := endpoint.Id()
			endpoints[id] = endpoint
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
		//gotServices := make(EndpointSet)
		//addServices := remoteEndpoints.Union(gotServices).Subtract(gotServices)
		//delServices := remoteEndpoints.Union(gotServices).Subtract(remoteEndpoints)

		// We only create tunnels for remote endpoints that Serf believes
		// to be alive, since if Serf isn't working we expect that OpenVPN
		// won't work either.
		gotTunnels := make(EndpointSet)
		exitingTunnels := make(EndpointSet)

		for _, tunnel := range tunnelState.Tunnels {
			id := tunnel.EndpointId
			gotTunnels.Add(id)
			if tunnel.State == VPNExiting {
				exitingTunnels.Add(id)
			}
		}

		addTunnels := liveRemoteEndpoints.Union(gotTunnels).Subtract(gotTunnels)
		delTunnels := liveRemoteEndpoints.Union(gotTunnels).Subtract(liveRemoteEndpoints).Subtract(exitingTunnels)

		log.Printf("All remote endpoints: %#v", remoteEndpoints)
		log.Printf("All live remote endpoints: %#v", liveRemoteEndpoints)
		//log.Printf("Add Consul services for %#v", addServices)
		//log.Printf("Remove Consul services for %#v", delServices)
		log.Printf("Current tunnels %#v", gotTunnels)
		log.Printf("Add tunnels for %#v", addTunnels)
		log.Printf("Remove tunnels for %#v", delTunnels)

		for endpointId := range addTunnels {
			err := tunnelMgr.StartTunnel(endpoints[endpointId])
			if err != nil {
				log.Printf("Failed to start tunnel to endpoint %s: %s", endpointId, err)
				continue
			}
		}
		for endpointId := range delTunnels {
			err := tunnelMgr.CloseTunnel(endpointId)
			if err != nil {
				log.Printf("Failed to signal endpoint %s tunnel to close: %s", endpointId, err)
				continue
			}
		}

		if !timeout.Stop() {
			<-timeout.C
		}
		timeout.Reset(refreshTime)

		// Now block here until the situation changes somehow.
		// Both the Serf cluster and the OpenVPN tunnel statuses can change;
		// either will cause us to re-evaluate our whole configuration and
		// make changes to "repair" any inconsistencies between expected
		// and actual states.
		select {
		case clusterState = <-clusterStateCh:
			log.Printf("Cluster state changed %#v", clusterState)
		case tunnelState = <-tunnelStateCh:
			log.Printf("Tunnel state changed %#v", tunnelState)
		case <-timeout.C:
			log.Println("Periodic refresh")
		}

	}
}

func interfaceIPAddr(name string) (string, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", fmt.Errorf("failed to read %s interface config: %s", name)
	}

	localAddrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("failed to enumerate addresses for %s: %s", name, err)
	}
	if len(localAddrs) == 0 {
		return "", fmt.Errorf("%s has no addresses: %s", name, err)
	}

	ipv4AddrCount := 0
	var localAddr string
	for _, addr := range localAddrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			ipv4Addr := ipNet.IP.To4()
			if ipv4Addr == nil {
				continue
			}
			if localAddr == "" {
				localAddr = ipv4Addr.String()
			}
			ipv4AddrCount = ipv4AddrCount + 1
		}
	}

	if ipv4AddrCount == 0 {
		return "", fmt.Errorf("%s has no IPv4 addresses: %s", name, err)
	}

	log.Printf("%s address is %s", name, localAddr)
	if ipv4AddrCount > 1 {
		log.Printf("%s has multiple IPv4 addresses, so I just picked one arbitrarily", name)
	}

	return localAddr, nil
}
