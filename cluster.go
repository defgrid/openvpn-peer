package main

import (
	"sort"

	"github.com/hashicorp/serf/serf"
)

type ClusterState struct {
	RemoteEndpoints []*Endpoint
	LocalEndpoints  []*Endpoint
	ThisEndpoint    *Endpoint
}

func newClusterState(gossip *Gossip, members []serf.Member) *ClusterState {
	ret := &ClusterState{
		RemoteEndpoints: make([]*Endpoint, 0, 5),
		LocalEndpoints:  make([]*Endpoint, 0, 5),
	}

	localNode := gossip.localNode()
	localEndpoint := newEndpoint(gossip, localNode)

	myRegionId := localEndpoint.RegionId()
	myNodeName := localEndpoint.NodeName()

	ret.ThisEndpoint = localEndpoint

	for _, member := range members {
		if member.Name == myNodeName {
			// We already took care of our own endpoint object
			continue
		}

		endpoint := newEndpoint(gossip, &member)

		if myRegionId == endpoint.RegionId() {
			ret.LocalEndpoints = append(ret.LocalEndpoints, endpoint)
		} else {
			ret.RemoteEndpoints = append(ret.RemoteEndpoints, endpoint)
		}
	}

	sort.Stable(ret.SortByDistance(ret.LocalEndpoints))

	// Don't really have any need for these to be in order, but let's
	// do it anyway to be consistent, since we should never have
	// more than tens of these.
	sort.Stable(ret.SortByDistance(ret.RemoteEndpoints))

	return ret
}

func (s *ClusterState) SortByDistance(endpoints []*Endpoint) EndpointSorter {
	return EndpointSorter{
		local:     s.ThisEndpoint,
		endpoints: endpoints,
	}
}

type EndpointSorter struct {
	local     *Endpoint
	endpoints []*Endpoint
}

func (s EndpointSorter) Len() int {
	return len(s.endpoints)
}

func (s EndpointSorter) Less(i, j int) bool {
	return s.local.DistanceTo(s.endpoints[i]) < s.local.DistanceTo(s.endpoints[j])
}

func (s EndpointSorter) Swap(i, j int) {
	temp := s.endpoints[i]
	s.endpoints[i] = s.endpoints[j]
	s.endpoints[j] = temp
}
