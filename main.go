package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"text/tabwriter"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/hashicorp/serf/serf"
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

	serfConfig := serf.DefaultConfig()
	serfConfig.MemberlistConfig = memberlist.DefaultWANConfig()

	iface, err := net.InterfaceByName(config.LocalInterface)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read %s interface config: %s\n", config.LocalInterface, err)
		os.Exit(2)
	}

	localAddrs, err := iface.Addrs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to enumerate addresses for %s: %s\n", config.LocalInterface, err)
		os.Exit(2)
	}
	if len(localAddrs) == 0 {
		fmt.Fprintf(os.Stderr, "%s has no addresses: %s\n", config.LocalInterface, err)
		os.Exit(2)
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
		fmt.Fprintf(os.Stderr, "%s has no IPv4 addresses: %s\n", config.LocalInterface, err)
		os.Exit(2)
	}

	log.Printf("Local IP address is %#s", localAddr)
	if ipv4AddrCount > 1 {
		log.Printf("%s has multiple IPv4 addresses, so I just picked one arbitrarily", config.LocalInterface)
	}

	serfConfig.MemberlistConfig.BindAddr = localAddr
	serfConfig.MemberlistConfig.BindPort = config.GossipPort
	serfConfig.MemberlistConfig.AdvertiseAddr = config.PublicIPAddress
	serfConfig.MemberlistConfig.AdvertisePort = config.GossipPort
	serfConfig.NodeName = config.NodeName
	serfConfig.Tags = map[string]string{
		"int_ip": localAddr,
	}
	serfConfig.SnapshotPath = config.DataDir
	serfConfig.CoalescePeriod = 3 * time.Second
	serfConfig.QuiescentPeriod = time.Second
	serfConfig.UserCoalescePeriod = 3 * time.Second
	serfConfig.UserQuiescentPeriod = time.Second
	serfConfig.EnableNameConflictResolution = true
	serfConfig.RejoinAfterLeave = true

	eventCh := make(chan serf.Event, 512)
	serfConfig.EventCh = eventCh

	log.Println("starting serf...")
	serf, err := serf.Create(serfConfig)
	if err != nil {
		panic(err)
	}

	shutdownCh := serf.ShutdownCh()

	// We'll always try to join the local agent, since that both tests that
	// our public IP is working as expected and increases the likelihood
	// of this initial join succeeding.
	initialPeers := append(config.InitialPeers, fmt.Sprintf("%s:%d", config.PublicIPAddress, config.GossipPort))
	joined, err := serf.Join(initialPeers, false)
	if err != nil {
		log.Printf("Initial join failed: %s", err)
	} else {
		log.Printf("Joined a cluster by contacting %d nodes", joined)
	}

	for {
		select {
		case e := <-eventCh:
			log.Printf("recieved event %s", e)
			PrintMembers(serf.Members())
		case <-shutdownCh:
			log.Println("serf is shutting down")
			break
		}
	}
}

func PrintMembers(members []serf.Member) {
	w := tabwriter.NewWriter(os.Stdout, 4, 4, 2, ' ', 0)
	w.Write([]byte("\nname\tglobal address\tlocal address\tstatus\t\n"))
	for _, member := range members {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s:%d\t%s\t%s\t\n",
			member.Name,
			member.Addr,
			member.Port,
			member.Tags["int_ip"],
			member.Status,
		)))
	}
	w.Flush()
	os.Stdout.Write([]byte{'\n'})
}
