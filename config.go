package main

import (
	"fmt"
	"io/ioutil"

	"github.com/hashicorp/hcl"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	NodeName             string   `hcl:"node_name" envconfig:"OPENVPN_PEER_NODE_NAME"`
	LocalInterface       string   `hcl:"local_interface" envconfig:"OPENVPN_PEER_INTERFACE"`
	CommonPrefixLen      int      `hcl:"common_prefix_length" envconfig:"OPENVPN_PEER_COMMON_PREFIX_LEN"`
	RegionPrefixLen      int      `hcl:"region_prefix_length" envconfig:"OPENVPN_PEER_REGION_PREFIX_LEN"`
	DCPrefixLen          int      `hcl:"datacenter_prefix_length" envconfig:"OPENVPN_PEER_DC_PREFIX_LEN"`
	PublicIPAddress      string   `hcl:"public_ip_address" envconfig:"OPENVPN_PEER_PUBLIC_IP"`
	VPNEndpointStartPort int      `hcl:"vpn_endpoint_start_port" envconfig:"OPENVPN_PEER_START_PORT"`
	GossipPort           int      `hcl:"gossip_port" envconfig:"OPENVPN_PEER_GOSSIP_PORT"`
	GossipEncryptionKey  string   `hcl:"gossip_encryption_key" envconfig:"OPENVPN_PEER_GOSSIP_KEY"`
	DataDir              string   `hcl:"data_dir" envconfig:"OPENVPN_PEER_DATA_DIR"`
	InitialPeers         []string `hcl:"initial_peers"`
}

func ConfigFromFile(filename string) (*Config, error) {
	sourceBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading %s: %s", filename, err)
	}

	ret := &Config{}
	err = hcl.Unmarshal(sourceBytes, ret)
	if err != nil {
		return nil, fmt.Errorf("error parsing %s: %s", filename, err)
	}

	return ret, nil
}

func ConfigFromEnv() (*Config, error) {
	ret := &Config{}
	err := envconfig.Process("openvpn-peer", ret)
	return ret, err
}

func LoadConfig(filename string) (*Config, error) {
	envCfg, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}

	fileCfg, err := ConfigFromFile(filename)
	if err != nil {
		return nil, err
	}

	envCfg.Override(fileCfg)
	return envCfg, nil
}

func (c *Config) Override(other *Config) {
	if other.NodeName != "" {
		c.NodeName = other.NodeName
	}
	if other.LocalInterface != "" {
		c.LocalInterface = other.LocalInterface
	}
	if other.CommonPrefixLen != 0 {
		c.CommonPrefixLen = other.CommonPrefixLen
	}
	if other.RegionPrefixLen != 0 {
		c.RegionPrefixLen = other.RegionPrefixLen
	}
	if other.DCPrefixLen != 0 {
		c.DCPrefixLen = other.DCPrefixLen
	}
	if other.PublicIPAddress != "" {
		c.PublicIPAddress = other.PublicIPAddress
	}
	if other.VPNEndpointStartPort != 0 {
		c.VPNEndpointStartPort = other.VPNEndpointStartPort
	}
	if other.GossipPort != 0 {
		c.GossipPort = other.GossipPort
	}
	if other.GossipEncryptionKey != "" {
		c.GossipEncryptionKey = other.GossipEncryptionKey
	}
	if other.DataDir != "" {
		c.DataDir = other.DataDir
	}
	if other.InitialPeers != nil && len(other.InitialPeers) > 0 {
		c.InitialPeers = other.InitialPeers
	}
}
