package main

import (
	"net"

	"github.com/hashicorp/serf/coordinate"
	"github.com/hashicorp/serf/serf"
)

type Endpoint struct {
	config *Config
	member *serf.Member
	coord  *coordinate.Coordinate
}

const MaxDistance = int64((1 << 63) - 1)

func newEndpoint(gossip *Gossip, member *serf.Member) *Endpoint {
	ret := &Endpoint{
		config: gossip.config,
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
	return net.ParseIP(e.member.Tags["int_ip"])
}

func (e *Endpoint) Status() serf.MemberStatus {
	return e.member.Status
}

func (e *Endpoint) RegionId() string {
	prefixLen := e.config.RegionPrefixLen
	ip := e.InternalAddr()
	if ip == nil {
		// If we don't have an IP address then we don't have a region either
		return ""
	}

	mask := net.CIDRMask(prefixLen, 32)
	return ip.Mask(mask).String()
}

func (e *Endpoint) DatacenterId() string {
	prefixLen := e.config.DCPrefixLen
	ip := e.InternalAddr()
	if ip == nil {
		// If we don't have an IP address then we don't have a region either
		return ""
	}

	mask := net.CIDRMask(prefixLen, 32)
	return ip.Mask(mask).String()
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
