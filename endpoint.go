package main

import (
	"fmt"
	"net"

	"github.com/hashicorp/serf/coordinate"
	"github.com/hashicorp/serf/serf"
)

type Endpoint struct {
	addr   Address
	member *serf.Member
	coord  *coordinate.Coordinate
}

const MaxDistance = int64((1 << 63) - 1)

func newEndpoint(gossip *Gossip, member *serf.Member) *Endpoint {
	addr := gossip.config.Addressing.Address(member.Tags["int_ip"])

	ret := &Endpoint{
		addr:   addr,
		member: member,
	}

	if coord, ok := gossip.serf.GetCachedCoordinate(member.Name); ok {
		ret.coord = coord
	}

	return ret
}

func (e *Endpoint) NodeName() string {
	return e.member.Name
}

func (e *Endpoint) GossipAddr() net.IP {
	return e.member.Addr
}

func (e *Endpoint) GossipPort() uint16 {
	return e.member.Port
}

func (e *Endpoint) InternalAddr() net.IP {
	return e.addr.IP
}

func (e *Endpoint) Status() serf.MemberStatus {
	return e.member.Status
}

func (e *Endpoint) Alive() bool {
	return e.member.Status == serf.StatusAlive
}

func (e *Endpoint) ExpectedAlive() bool {
	return e.member.Status != serf.StatusLeft && e.member.Status != serf.StatusLeaving
}

func (e *Endpoint) Address() Address {
	return e.addr
}

func (e *Endpoint) RegionId() string {
	return e.addr.RegionId()
}

func (e *Endpoint) DatacenterId() string {
	return e.addr.DatacenterId()
}

func (e *Endpoint) Id() EndpointId {
	return e.addr.EndpointId()
}

// DistanceTo returns the "round-trip distance" to/from the other
// given endpoint.
//
// Distances don't mean anything in absolute terms, but they can be
// used to sort endpoints into a "closest first" list.
//
// If either endpoint does not yet have a network coordinate, this will
// return the largest possible int64 as an approximation of "infinity".
func (e *Endpoint) DistanceTo(other *Endpoint) int64 {
	if e == nil || other == nil {
		return MaxDistance
	}
	if e.coord == nil || other.coord == nil {
		return MaxDistance
	}

	return int64(e.coord.DistanceTo(other.coord))
}

type EndpointId uint16

const InvalidEndpointId EndpointId = 0xffff

func (id EndpointId) String() string {
	if id == InvalidEndpointId {
		return "???"
	} else {
		return fmt.Sprintf("%03x", uint16(id))
	}
}

// EndpointSet represents a set of endpoints -- or rather, of endpoint ids.
//
// This is just a utility used to easily recognize the difference between
// the current state and the desired state, as the first step towards
// implementing the desired state.
type EndpointSet map[EndpointId]struct{}

func (s EndpointSet) Add(id EndpointId) {
	if id == InvalidEndpointId {
		// Can't add an invalid endpoint id to a set
		return
	}
	s[id] = struct{}{}
}

func (s EndpointSet) Remove(id EndpointId) {
	delete(s, id)
}

func (s EndpointSet) Has(id EndpointId) bool {
	_, ok := s[id]
	return ok
}

func (s EndpointSet) Union(other EndpointSet) EndpointSet {
	ret := make(EndpointSet, len(s)+len(other))

	for k, v := range s {
		ret[k] = v
	}
	for k, v := range other {
		ret[k] = v
	}

	return ret
}

func (s EndpointSet) Subtract(other EndpointSet) EndpointSet {
	ret := make(EndpointSet, len(s))

	for k, _ := range s {
		if !other.Has(k) {
			ret.Add(k)
		}
	}

	return ret
}
