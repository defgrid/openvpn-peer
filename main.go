package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"text/tabwriter"
)

func main() {

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

	mgr, err := NewManager(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n\n", err)
		os.Exit(2)
	}

	mgr.Run()

}

func PrintState(state *ClusterState) {
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
