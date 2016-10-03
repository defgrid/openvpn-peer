package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
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
	initialState := <-changeCh
	PrintState(initialState)

	joined, err := gossip.Join(config.InitialPeers)
	if err != nil {
		log.Printf("Initial join failed: %s", err)
	} else {
		log.Printf("Joined a cluster by contacting %d nodes", joined)
	}

	for {
		select {
		case state := <-changeCh:
			PrintState(state)
		}
	}
}

func PrintState(state *ClusterState) {
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 2, ' ', 0)
	w.Write([]byte("\nname\tglobal address\tlocal address\tregion\tdatacenter\tdistance\tstatus\t\n"))

	printEndpoint := func(e *Endpoint) {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s:%d\t%s\t%s\t%s\t%d\t%s\t\n",
			e.NodeName(),
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
