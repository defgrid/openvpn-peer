package main

import (
	"fmt"
	"log"
	"net"
	"sync"
)

type TunnelsState struct {
	Tunnels []*Tunnel
}

type Tunnel struct {
	EndpointId EndpointId
	State      VPNState
}

func newTunnelsState(vpnStates map[EndpointId]VPNState) *TunnelsState {
	tunnels := make([]*Tunnel, 0, len(vpnStates))

	for endpointId, state := range vpnStates {
		tunnels = append(tunnels, &Tunnel{
			EndpointId: endpointId,
			State:      state,
		})
	}

	return &TunnelsState{
		Tunnels: tunnels,
	}
}

type TunnelMgr struct {
	// lock must be held when reading/writing either of the
	// tunnel maps below.
	lock sync.RWMutex

	tunnelVPNs   map[EndpointId]*OpenVPN
	tunnelStates map[EndpointId]VPNState

	changeCh chan<- *TunnelsState

	localEndpoint  *Endpoint
	secretFilename string
}

type TunnelMgrConfig struct {
	SecretFilename string
	LocalEndpoint  *Endpoint
}

func NewTunnelMgr(config *TunnelMgrConfig, changeCh chan<- *TunnelsState) *TunnelMgr {
	return &TunnelMgr{
		tunnelVPNs:     make(map[EndpointId]*OpenVPN),
		tunnelStates:   make(map[EndpointId]VPNState),
		changeCh:       changeCh,
		localEndpoint:  config.LocalEndpoint,
		secretFilename: config.SecretFilename,
	}
}

func (m *TunnelMgr) StartTunnel(endpoint *Endpoint) error {
	if m == nil {
		return fmt.Errorf("can't start tunnel on nil TunnelMgr")
	}
	m.lock.Lock()
	defer m.lock.Unlock()

	endpointId := endpoint.Id()
	if m.tunnelVPNs[endpointId] != nil {
		// We already have a tunnel for this endpoint, so there's
		// nothing to do here.
		return fmt.Errorf("already have tunnel for endpoint %s", endpointId)
	}

	localAddr := m.localEndpoint.Address()

	localPort, remotePort := localAddr.VPNEndpointPorts(endpointId)
	localTunnelIP, remoteTunnelIP := localAddr.TunnelInternalIPs(endpointId)

	listenIPAddr := localAddr.IP
	remoteIPAddr := endpoint.GossipAddr()

	vpn, err := StartOpenVPN(&VPNConfig{
		// TODO: These should be configurable
		OpenVPNPath:  "/usr/sbin/openvpn",
		LauncherPath: "/usr/bin/sudo",

		RemoteAddr: &net.UDPAddr{
			IP:   remoteIPAddr,
			Port: remotePort,
		},
		LocalAddr: &net.UDPAddr{
			IP:   listenIPAddr,
			Port: localPort,
		},

		SecretFilename: m.secretFilename,

		TunnelRemoteAddr: remoteTunnelIP,
		TunnelLocalAddr:  localTunnelIP,
	})
	if err != nil {
		return err
	}

	m.tunnelVPNs[endpointId] = vpn
	m.tunnelStates[endpointId] = VPNLaunching

	go func() {
		var state VPNState
		for state != VPNExited {
			state = vpn.AwaitStateChange()
			log.Printf("VPN to endpoint %s changed state to %d", endpointId, state)
			m.lock.Lock()
			if state == VPNExited {
				delete(m.tunnelVPNs, endpointId)
				delete(m.tunnelStates, endpointId)
			} else {
				m.tunnelStates[endpointId] = state
			}
			notification := newTunnelsState(m.tunnelStates)
			m.lock.Unlock()
			m.changeCh <- notification
		}
	}()

	return nil
}

func (m *TunnelMgr) CloseTunnel(endpointId EndpointId) error {
	// We're just going to signal the tunnel to stop, so we
	// are just going to read our maps. Later our monitoring
	// goroutine will see that it exited and clean up before
	// signalling that the tunnel is closed.
	m.lock.RLock()
	defer m.lock.RUnlock()

	vpn := m.tunnelVPNs[endpointId]
	if vpn == nil {
		// Nothing to do
		return nil
	}

	return vpn.Close()
}

func (m *TunnelMgr) HasTunnel(endpointId EndpointId) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return m.tunnelVPNs[endpointId] != nil
}
