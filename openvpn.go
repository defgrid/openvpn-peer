package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/apparentlymart/go-openvpn-mgmt/openvpn"
)

type OpenVPN struct {
	cmd     *exec.Cmd
	mgmt    *openvpn.MgmtClient
	eventCh <-chan openvpn.Event

	stateCh chan VPNState
}

type VPNState int

const (
	// VPNLaunching is the initial state, where we've launched
	// the OpenVPN process but it hasn't yet started connecting to
	// the remote endpoint.
	VPNLaunching VPNState = iota

	// VPNConnecting indicates either that the process is attempting
	// connection for the first time or is on its first attempt at
	// reconnecting after getting disconnected. If a connection attempt
	// fails, state will change to VPNRetrying each time it retries
	// the connection.
	//
	// This distinction is made because intermittent disconnections are
	// normal and expected (the Internet is a fickle connection medium)
	// but sustained failure is worthy of more extreme measures such
	// as paging a human for help or automatically reconfiguring the
	// network's core router(s) to route packets elsewhere.
	//
	// The intent is that "Connecting" would be a "Warning" condition
	// for monitoring purposes, while "Retrying" would be "Critical".
	// VPNRetrying will be emitted repeatedly if the OpenVPN process
	// retries multiple times without success.

	VPNConnecting
	VPNRetrying

	// VPNConnected indicates that the tunnel is active.
	VPNConnected

	// VPNExiting indicates that the OpenVPN process is shutting down.
	// The VPNExited event should follow shortly after this.
	VPNExiting

	// VPNExited will always be the new state of the final state
	// change event before the event channel is closed.
	VPNExited
)
//go:generate stringer -type=VPNState

type VPNConfig struct {

	// OpenVPNPath is the path to the OpenVPN executable
	OpenVPNPath string

	// LauncherPath is the path to an executable that will be used
	// to launch OpenVPN. The only reasonable values for this field
	// are the empty string (which disables using a launcher altogether)
	// or a path to the program "sudo".
	LauncherPath string

	// RemoteAddr and LocalAddr specify the ports where the OpenVPN processes
	// will listen on the remote peer and the local peer respectively.
	// RemoteAddr is used to instruct OpenVPN what to connect *to*, while
	// LocalAddr is used to instruct OpenVPN where to listen for incoming
	// connections.
	//
	// The tunnels we create are peer-to-peer, so both sides will actively
	// try to reach the other until one succeeds.
	RemoteAddr *net.UDPAddr
	LocalAddr  *net.UDPAddr

	// SecretFilename is the path to the file where the pre-shared key is
	// stored.
	//
	// A suitable file can be generated using:
	//
	//    openvpn --genkey --secret secret.key
	//
	// All endpoints must use the same key.
	SecretFilename string

	// TunnelRemoteAddr and TunnelLocalAddr specify the IP addresses that
	// will be used to represent the two endpoints *within* the tunnel.
	TunnelRemoteAddr net.IP
	TunnelLocalAddr  net.IP
}

// StartOpenVPN launches OpenVPN as a child process and instructs it
// to connect to a management socket so we can control it and get
// notified when the connection status changes.
//
// This function returns once the OpenVPN process has launched and
// successfully connected to the management port. Thus any returned
// instance is ready to be "managed" and the returned error will
// signal any problems that occur during startup.
//
// The OpenVPN process will go on running in the background after
// this function returns, until it either exits of its own accord
// or it is explicitly terminated with the Close method.
func StartOpenVPN(config *VPNConfig) (*OpenVPN, error) {

	// Forewarning: this function is kinda hairy. Coordinating the sequence
	// of events here and handling all the errors along the way is rather
	// tricky. The "happy path" steps are:
	// - start a management listener
	// - launch OpenVPN as a child process, telling it to connect to management
	// - wait for the process to connect to management
	// - open the management client and event channel
	//
	// In case of any failure we must make sure not to leave any dangling
	// child processes or goroutines. If you find any in here then that's
	// always a bug to be fixed.

	mgmtSocketDir, err := ioutil.TempDir("", "openvpn-peer")
	if err != nil {
		return nil, fmt.Errorf("failed to create tempdir for socket: %s", err)
	}

	// Remove the socket once we're done with this function. On exit we've
	// either failed or the OpenVPN process has already connected, so it's
	// safe to remove the socket's directory entry in either case.
	defer os.RemoveAll(mgmtSocketDir)

	mgmtSocketPath := path.Join(mgmtSocketDir, "mgmt.sock")
	mgmtListener, err := openvpn.Listen(mgmtSocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open mgmt socket: %s", err)
	}

	var cmdLine = []string{
		config.LauncherPath,
		"--",
		config.OpenVPNPath,

		// ***** REMOVE THIS BEFORE RELEASING FOR PRODUCTION USE *****
		// Allow connections from any address, which is useful in dev
		// when running two nodes on the same machine where the IP addresses
		// tend to get a bit tangled up. But this weakens our security
		// for production use on the public internet.
		"--float",

		// Have OpenVPN connect to our management socket, and don't try
		// to connect until we're actively pumping the management event
		// stream.
		"--management-client",
		"--management", mgmtSocketPath, "unix",
		"--management-hold", // don't connect until we have set up mgmt conn

		// Secret
		"--secret", config.SecretFilename,

		// Network settings for the tunnel
		"--dev", "tun",
		"--local", config.LocalAddr.IP.String(),
		"--port", strconv.Itoa(config.LocalAddr.Port),
		"--remote", config.RemoteAddr.IP.String(), strconv.Itoa(config.RemoteAddr.Port),
		"--ifconfig", config.TunnelLocalAddr.String(), config.TunnelRemoteAddr.String(),

		// This means we will detect a tunnel failure after 30 seconds,
		// and send a keepalive every 15 so that we minimize the chance
		// of false positives. This also implies that we'll retry connecting
		// every 30 seconds in case of problems.
		//
		// With these timings, and assuming that a caller is using the
		// "VPNRetrying" state to signal a critical error, this means that
		// a tunnel gets 60 seconds to recover before it is considered to
		// be in a critical state. It also means that there can be up to
		// 30 seconds of packet loss before we notice a down tunnel and
		// start forwarding to a neighbor.
		"--keepalive", "15", "30",
	}

	// If we don't actually have a launcher, we'll run OpenVPN directly.
	if cmdLine[0] == "" {
		cmdLine = cmdLine[2:]
	}

	log.Printf("Starting OpenVPN %s", strings.Join(cmdLine, " "))

	cmd := &exec.Cmd{
		Path: cmdLine[0],
		Args: cmdLine,

		// Don't inherit environment
		Env: []string{},

		Dir: mgmtSocketDir,
	}

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("OpenVPN failed to start: %s", err)
	}

	type connMsg struct {
		conn *openvpn.IncomingConn
		err  error
	}

	// Wait for either a management socket connection or for the
	// OpenVPN process to exit. We'll do this using channels that
	// deliver only a single value before being closed.
	connCh := make(chan connMsg)
	exitCh := make(chan error)

	go func() {
		err := cmd.Wait()
		// Note that if OpenVPN connects successfully we will still end
		// up here *eventually* when it exists, potentially many days
		// after we initially launched the process. Thus we must remain
		// ready to read this channel as long as the OpenVPN process is
		// running, to avoid leaking this goroutine.
		exitCh <- err
		close(exitCh)
	}()
	go func() {
		conn, err := mgmtListener.Accept()
		connCh <- connMsg{conn, err}
		close(connCh)
	}()

	// Make sure we never leave the above goroutines dangling after
	// we exit.
	defer func() {
		// For each of these we will block until a value is generated and
		// then discard it, or return immediately if the channel is already
		// closed.
		go func() {
			<-connCh
		}()
		go func() {
			<-exitCh
		}()

		// Always close the management listener before exiting.
		// If we were still waiting in Accept() above, this will cause
		// that call to return and then its result will be consumed
		// by the <-connCh cleanup goroutine above.
		//
		// This is safe even if we succeed, because once the management
		// connection is established we don't need to be listening anymore.
		mgmtListener.Close()
	}()

	var conn *openvpn.IncomingConn

	select {
	case cs := <-connCh:
		// Either we got a connection on the management port, or there
		// was some sort of error while we were waiting.

		if cs.err != nil {
			// Don't leak a dangling child process.
			cmd.Process.Signal(os.Kill)
			return nil, fmt.Errorf("error awaiting mgmt connection: %s", cs.err)
		}

		// This is the happy path. We can get our connection and proceed
		// with using it.
		conn = cs.conn

	case err := <-exitCh:
		if err != nil {
			return nil, fmt.Errorf("OpenVPN failed to start: %s", err)
		} else {
			return nil, fmt.Errorf("OpenVPN exited prematurely")
		}

	case <-time.After(10 * time.Second):
		// Don't leak a dangling child process.
		// (our goroutine is still blocking on cmd.Wait() so it will
		// reap the process once it dies.)
		cmd.Process.Signal(os.Kill)

		return nil, fmt.Errorf("timeout waiting for OpenVPN to start up")
	}

	eventCh := make(chan openvpn.Event, 16)
	mgmt := conn.Open(eventCh)

	err = mgmt.SetStateEvents(true)
	if err != nil {
		cmd.Process.Signal(os.Kill)
		return nil, fmt.Errorf("failed to enable state events: %s", err)
	}

	// From this point on, since we know we have a socket connected to
	// the OpenVPN process we assume we can detect that the process has
	// exited by the closure of that socket, which in turn leads to
	// the closure of eventCh.

	stateCh := make(chan VPNState)

	go func() {
		// We write the "Launching" change first so that we'll block here
		// until a caller begins processing state change events.
		stateCh <- VPNLaunching

		// When we first start up we are already in the CONNECTING state
		// and on our first try.
		connectTries := 1
		stateCh <- VPNConnecting

		for event := range eventCh {
			switch e := event.(type) {

			case *openvpn.HoldEvent:
				err = mgmt.HoldRelease()
				if err != nil {
					log.Printf("[WARNING] failed to release management hold: %s", err)
					continue
				}

			case *openvpn.StateEvent:
				newOpenVPNState := e.NewState()
				log.Printf("OpenVPN process moved to state %s", newOpenVPNState)

				switch newOpenVPNState {
				case "CONNECTING", "RECONNECTING":
					newState := VPNConnecting
					if connectTries > 0 {
						newState = VPNRetrying
					}
					connectTries = connectTries + 1
					stateCh <- newState
				case "CONNECTED":
					connectTries = 0
					stateCh <- VPNConnected
				case "EXITING":
					stateCh <- VPNExiting
				}
			}

		}

		stateCh <- VPNExited
		close(stateCh)
	}()

	return &OpenVPN{
		cmd:     cmd,
		mgmt:    mgmt,
		eventCh: eventCh,
		stateCh: stateCh,
	}, nil
}

// AwaitStateChange will block until the connected OpenVPN change state
// and will then return the new state.
//
// This function must be called frequently during the lifetime of the OpenVPN
// process, to allow OpenVPN event processing to continue.
//
// When the OpenVPN process exits (whether due to an intentional call to
// Close or ForceClose, or due to some external factor) this function will
// return VPNExited. If called again after this, the function will
// *immediately* return VPNExited without blocking.
func (o *OpenVPN) AwaitStateChange() VPNState {
	newState, stillRunning := <-o.stateCh

	if !stillRunning {
		// Synthetic event to signal that the process has exited.
		return VPNExited
	} else {
		return newState
	}
}

// Close will signal the OpenVPN process to shut down cleanly.
//
// After calling this, a goroutine must continue to wait on state change
// events until the OpenVPNExited state is recieved.
func (o *OpenVPN) Close() error {
	return o.mgmt.SendSignal("SIGTERM")
}

// ForceClose will abruptly terminate the OpenVPN process.
//
// This will work only if the OpenVPN process is running as the same user
// as the calling process, since it sends SIGKILL to the process. If
// not running as the same user, use Close to politely request that
// OpenVPN should shut itself down.
//
// After calling this, a goroutine must continue to wait on state change
// events until the OpenVPNExited state is recieved.
func (o *OpenVPN) ForceClose() error {
	return o.cmd.Process.Signal(os.Kill)
}
