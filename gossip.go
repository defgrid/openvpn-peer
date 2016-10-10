package main

import (
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/hashicorp/serf/serf"
)

type Gossip struct {
	config      *GossipConfig
	serf        *serf.Serf
	latestState *ClusterState
}

type GossipConfig struct {
	NodeName        string
	ListenIPAddr    string
	AdvertiseIPAddr string
	Port            int
	DataDir         string
	Addressing      *Addressing
}

func NewGossip(config *GossipConfig) *Gossip {
	return &Gossip{
		config: config,
	}
}

func (g *Gossip) Started() bool {
	return g.serf != nil
}

// Start causes the gossip pool to be started and then starts processing
// events for it, emitting cluster state instances on the provided
// channel each time the cluster changes.
//
// This function returns only once we have left the gossip pool, so it
// should usually be run in a separate goroutine.
func (g *Gossip) Start(changeCh chan *ClusterState) error {
	if g.serf != nil {
		// should never happen
		panic("gossip alread started")
	}

	config := g.config

	serfConfig := serf.DefaultConfig()
	serfConfig.MemberlistConfig = memberlist.DefaultWANConfig()

	serfConfig.MemberlistConfig.BindAddr = config.ListenIPAddr
	serfConfig.MemberlistConfig.BindPort = config.Port
	serfConfig.MemberlistConfig.AdvertiseAddr = config.AdvertiseIPAddr
	serfConfig.MemberlistConfig.AdvertisePort = config.Port
	serfConfig.NodeName = config.NodeName
	serfConfig.Tags = map[string]string{
		"int_ip": config.ListenIPAddr,
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
		return fmt.Errorf("error initalizing serf gossip: %s", err)
	}

	shutdownCh := serf.ShutdownCh()

	g.serf = serf

	for {
		select {

		case e := <-eventCh:
			log.Printf("recieved event %s", e)
			newState := g.refreshState()
			changeCh <- newState

		case <-shutdownCh:
			log.Println("serf is shutting down")
			g.serf = nil
			return nil

		}
	}

}

func (g *Gossip) Join(addrs []string) (int, error) {
	return g.serf.Join(addrs, false)
}

func (g *Gossip) LatestClusterState() *ClusterState {
	return g.latestState
}

func (g *Gossip) localNode() *serf.Member {
	member := g.serf.LocalMember()
	return &member
}

func (g *Gossip) refreshState() *ClusterState {
	members := g.serf.Members()
	newState := newClusterState(g, members)
	g.latestState = newState
	return newState
}
