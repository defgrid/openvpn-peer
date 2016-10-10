package main

import (
	"fmt"
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

// EndpointId returns the unique identifier for this endpoint, which
// is made from the bits in the endpoint's private IP address
// between the common prefix and the datacenter prefix. In other words,
// it's the datacenter prefix address with the common prefix "trimmed off",
// giving a 10-bit number.
//
// EndpointId is unique as long as the user respects the constraint that
// there should be only one endpoint per datacenter. If not, behavior is
// undefined and tunnel instability is the likely result.
func (e *Endpoint) EndpointId() EndpointId {
	commonPrefixLen := e.config.CommonPrefixLen
	dcPrefixLen := e.config.DCPrefixLen
	ip := e.InternalAddr()
	if ip == nil {
		// If we don't have an IP address then we don't have an endpoint id
		// either, so we'll return an invalid placeholder.
		return 0xffff
	}

	// These cases should've been caught during config validation, so
	// we won't go out of our way to report it but we will check
	// so that we won't crash if these assumptions are violated.
	if commonPrefixLen >= 24 || commonPrefixLen >= dcPrefixLen || (dcPrefixLen-commonPrefixLen) > 10 {
		return 0xffff
	}

	// First we'll compute the datacenter id as an IP address, and
	// extract the raw bytes from it.
	mask := net.CIDRMask(dcPrefixLen, 32)
	dcBytes := []byte(ip.Mask(mask))

	// integer division by 8 gives us the index of the byte that
	// contains the first bit of our id. Then modulo 8 tells us
	// how far we need to shift to move the first bit into the
	// MSB of the byte.
	firstByteIdx := commonPrefixLen / 8
	firstByteShiftOffset := uint(commonPrefixLen % 8)
	firstByteBits := 8 - firstByteShiftOffset
	secondByteBits := 10 - firstByteBits

	id := (uint16(dcBytes[firstByteIdx])<<(firstByteShiftOffset+2) |
		uint16(dcBytes[firstByteIdx+1])>>uint(8-secondByteBits))

	// Now we extract and shift the bits around so that they
	// occupy the low 10 bits of our return value.
	return EndpointId(id & 0x3ff)
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
		//return fmt.Sprintf("%010b", uint16(id))
	}
}
