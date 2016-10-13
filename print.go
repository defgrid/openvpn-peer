package main

import (
	"fmt"
	"os"
	"text/tabwriter"
)

// This file contains some functions that are able to print out
// our state objects in a human-readable way for debug purposes.

func PrintClusterState(state *ClusterState) {
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 2, ' ', 0)
	w.Write([]byte("\nname\teid\tglobal address\tlocal address\tregion\tdatacenter\tdistance\tstatus\t\n"))

	printEndpoint := func(e *Endpoint) {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s\t%s:%d\t%s\t%s\t%s\t%d\t%s\t\n",
			e.NodeName(),
			e.Id(),
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

func PrintTunnelState(state *TunnelsState) {
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 2, ' ', 0)
	w.Write([]byte("\neid\tstate\t\n"))

	for _, tunnel := range state.Tunnels {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s\t\n",
			tunnel.EndpointId,
			tunnel.State,
		)))
	}

	w.Flush()

	if len(state.Tunnels) == 0 {
		os.Stdout.Write([]byte("(no active tunnels)\n"))
	}

	os.Stdout.Write([]byte{'\n'})
}

