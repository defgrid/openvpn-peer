package main

import (
	"net"
)

type Addressing struct {
	CommonPrefixLen int
	RegionPrefixLen int
	DCPrefixLen     int
	LocalIPAddr     net.IP

	VPNEndpointStartPort int
}

func (ing *Addressing) Address(addr string) Address {
	ip := net.ParseIP(addr)
	return Address{ing, ip}
}

func (ing *Addressing) IPAddress(addr net.IP) Address {
	return Address{ing, addr}
}

func (ing *Addressing) LocalAddress() Address {
	return Address{ing, ing.LocalIPAddr}
}

type Address struct {
	ing *Addressing
	IP  net.IP
}

func (addr Address) String() string {
	return addr.IP.String()
}

func (addr Address) RegionId() string {
	prefixLen := addr.ing.RegionPrefixLen
	if addr.IP == nil {
		// If we don't have an IP address then we don't have a region either
		return ""
	}

	mask := net.CIDRMask(prefixLen, 32)
	return addr.IP.Mask(mask).String()
}

func (addr Address) DatacenterId() string {
	prefixLen := addr.ing.DCPrefixLen
	if addr.IP == nil {
		// If we don't have an IP address then we don't have a region either
		return ""
	}

	mask := net.CIDRMask(prefixLen, 32)
	return addr.IP.Mask(mask).String()
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
func (addr Address) EndpointId() EndpointId {
	commonPrefixLen := addr.ing.CommonPrefixLen
	dcPrefixLen := addr.ing.DCPrefixLen
	ip := addr.IP
	if ip == nil {
		// If we don't have an IP address then we don't have an endpoint id
		// either, so we'll return an invalid placeholder.
		return InvalidEndpointId
	}

	// These cases should've been caught during config validation, so
	// we won't go out of our way to report it but we will check
	// so that we won't crash if these assumptions are violated.
	if commonPrefixLen >= 24 || commonPrefixLen >= dcPrefixLen || (dcPrefixLen-commonPrefixLen) > 10 {
		return InvalidEndpointId
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

func (addr Address) TunnelInternalIPs(remoteId EndpointId) (local net.IP, remote net.IP) {
	localId := addr.EndpointId()

	// Start with 172.16.0.0/12. The remaining 20 bits will come from
	// the local and remote endpoint ids, which are 10 bits each.
	rawBaseAddr := (uint32(172) << 24) | (uint32(16) << 16)

	rawLocalAddr := rawBaseAddr | (uint32(localId) << 10) | uint32(remoteId)
	rawRemoteAddr := rawBaseAddr | (uint32(remoteId) << 10) | uint32(localId)

	localAddr := net.IPv4(
		byte(rawLocalAddr>>24),
		byte(rawLocalAddr>>16),
		byte(rawLocalAddr>>8),
		byte(rawLocalAddr>>0),
	)
	remoteAddr := net.IPv4(
		byte(rawRemoteAddr>>24),
		byte(rawRemoteAddr>>16),
		byte(rawRemoteAddr>>8),
		byte(rawRemoteAddr>>0),
	)

	return localAddr, remoteAddr
}

func (addr Address) VPNEndpointPorts(remoteId EndpointId) (int, int) {
	offset := addr.ing.VPNEndpointStartPort
	return int(addr.EndpointId()) + offset, int(remoteId) + offset
}
