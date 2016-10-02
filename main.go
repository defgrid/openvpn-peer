package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/hashicorp/serf/serf"
)

func main() {
	// Initial prototype of using serf, to start.

	startJoin := []string{"127.0.0.1:9020", "127.0.0.1:9021"}
	serfPort, err := strconv.Atoi(os.Args[1])
	if err != nil {
		panic(err)
	}

	serfConfig := serf.DefaultConfig()
	serfConfig.MemberlistConfig = memberlist.DefaultWANConfig()

	serfConfig.MemberlistConfig.BindAddr = "127.0.0.1"
	serfConfig.MemberlistConfig.BindPort = serfPort
	serfConfig.MemberlistConfig.AdvertiseAddr = "127.0.0.1"
	serfConfig.MemberlistConfig.AdvertisePort = serfPort
	serfConfig.NodeName = fmt.Sprintf("node-%d", serfPort)
	serfConfig.Tags = map[string]string{
		"region":     fmt.Sprintf("fake-region-%d", serfPort),
		"datacenter": fmt.Sprintf("fake-region-%da", serfPort),
		"prefix":     fmt.Sprintf("10.%d.0.0/16/24", serfPort-9000),
	}
	serfConfig.SnapshotPath = fmt.Sprintf("snap-%d", serfPort)
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

	joined, err := serf.Join(startJoin, false)
	if err != nil {
		panic(err)
	}

	log.Printf("Joined a cluster by contacting %d nodes", joined)

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
	w.Write([]byte("\nname\taddress\tregion\tdatacenter\tprefix\tstatus\t\n"))
	for _, member := range members {
		w.Write([]byte(fmt.Sprintf(
			"%s\t%s:%d\t%s\t%s\t%s\t%s\t\n",
			member.Name,
			member.Addr,
			member.Port,
			member.Tags["region"],
			member.Tags["datacenter"],
			member.Tags["prefix"],
			member.Status,
		)))
	}
	w.Flush()
	os.Stdout.Write([]byte{'\n'})
}
