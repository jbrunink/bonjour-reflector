package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"
)

func main() {
	// Read config file and generate mDNS forwarding maps
	configPath := flag.String("config", "", "Config file in TOML format")
	debug := flag.Bool("debug", false, "Enable pprof server on /debug/pprof/")
	flag.Parse()

	// Start debug server
	if *debug {
		go debugServer(6060)
	}

	cfg, err := readConfig(*configPath)
	if err != nil {
		log.Fatalf("Could not read configuration: %v", err)
	}
	poolsMap := mapByPool(cfg.Devices)

	// Get a handle on the network interface
	rawTraffic, err := pcap.OpenLive(cfg.NetInterface, 65536, true, time.Second)
	if err != nil {
		log.Fatalf("Could not find network interface: %v", cfg.NetInterface)
	}

	// Get the local MAC address, to filter out Bonjour packet generated locally
	intf, err := net.InterfaceByName(cfg.NetInterface)
	if err != nil {
		log.Fatal(err)
	}
	brMACAddress := intf.HardwareAddr

	// Get a channel of Bonjour packets to process
	decoder := gopacket.DecodersByLayerName["Ethernet"]
	source := gopacket.NewPacketSource(rawTraffic, decoder)
	bonjourPackets := filterBonjourPacketsLazily(source, brMACAddress)

	// Process Bonjours packets
	for bonjourPacket := range bonjourPackets {
		fmt.Println(bonjourPacket.packet.String())
		if bonjourPacket.vlanTag == nil {
			bonjourPacket.vlanTag = func(i uint16) *uint16 { return &i }(1)
		}
		// Forward the mDNS query or response to appropriate VLANs
		if bonjourPacket.isDNSQuery {
			tags, ok := poolsMap[*bonjourPacket.vlanTag]
			if !ok {
				continue
			}
			for _, tag := range tags {
				sendBonjourPacket(rawTraffic, &bonjourPacket, tag, brMACAddress)
			}
		} else {
			device, ok := cfg.Devices[macAddress(bonjourPacket.srcMAC.String())]
			if !ok {
				continue
			}
			for _, tag := range device.SharedPools {
				sendBonjourPacket(rawTraffic, &bonjourPacket, tag, brMACAddress)
			}
		}
	}
}

func debugServer(port int) {
	err := http.ListenAndServe(fmt.Sprintf("localhost:%d", port), nil)
	if err != nil {
		log.Fatalf("The application was started with -debug flag but could not listen on port %v: \n %s", port, err)
	}
}
