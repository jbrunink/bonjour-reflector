package main

import (
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

type bonjourPacket struct {
	packet     gopacket.Packet
	srcMAC     *net.HardwareAddr
	dstMAC     *net.HardwareAddr
	isIPv6     bool
	vlanTag    *uint16
	isDNSQuery bool
}

func filterBonjourPacketsLazily(source *gopacket.PacketSource, brMACAddress net.HardwareAddr) chan bonjourPacket {
	// Process packets, and forward Bonjour traffic to the returned channel

	// Set decoding to Lazy
	source.DecodeOptions = gopacket.DecodeOptions{Lazy: true}

	packetChan := make(chan bonjourPacket, 100)

	go func() {
		for packet := range source.Packets() {
			tag := parseVLANTag(packet)

			// Do not process packets generated by this daemon
			srcMAC, dstMAC := parseEthernetLayer(packet)
			if srcMAC.String() == brMACAddress.String() {
				continue
			}

			// Only process packets sent to one of the multicast IP addresses specified in RFC 6762
			dstIP, isIPv6 := parseIPLayer(packet)
			if dstIP.String() != "224.0.0.251" && dstIP.String() != "ff02::fb" {
				continue
			}

			// Only process packets sent to the UDP port dedicated to mDNS
			dstPort, payload := parseUDPLayer(packet)
			if dstPort != 5353 {
				continue
			}

			isDNSQuery := parseDNSPayload(payload)

			// Pass on the packet for its next adventure
			packetChan <- bonjourPacket{
				packet:     packet,
				vlanTag:    tag,
				srcMAC:     srcMAC,
				dstMAC:     dstMAC,
				isIPv6:     isIPv6,
				isDNSQuery: isDNSQuery,
			}
		}
	}()

	return packetChan
}

func parseEthernetLayer(packet gopacket.Packet) (srcMAC, dstMAC *net.HardwareAddr) {
	if parsedEth := packet.Layer(layers.LayerTypeEthernet); parsedEth != nil {
		srcMAC = &parsedEth.(*layers.Ethernet).SrcMAC
		dstMAC = &parsedEth.(*layers.Ethernet).DstMAC
	}
	return
}

func parseVLANTag(packet gopacket.Packet) (tag *uint16) {
	if parsedTag := packet.Layer(layers.LayerTypeDot1Q); parsedTag != nil {
		tag = &parsedTag.(*layers.Dot1Q).VLANIdentifier
	}
	return
}

func parseIPLayer(packet gopacket.Packet) (dstIP net.IP, isIPv6 bool) {
	if parsedIP := packet.Layer(layers.LayerTypeIPv4); parsedIP != nil {
		dstIP = parsedIP.(*layers.IPv4).DstIP
		isIPv6 = false
	}
	if parsedIP := packet.Layer(layers.LayerTypeIPv6); parsedIP != nil {
		dstIP = parsedIP.(*layers.IPv6).DstIP
		isIPv6 = true
	}
	return
}

func parseUDPLayer(packet gopacket.Packet) (dstPort layers.UDPPort, payload []byte) {
	if parsedUDP := packet.Layer(layers.LayerTypeUDP); parsedUDP != nil {
		dstPort = parsedUDP.(*layers.UDP).DstPort
		payload = parsedUDP.(*layers.UDP).Payload
	}
	return
}

func parseDNSPayload(payload []byte) (isDNSQuery bool) {
	packet := gopacket.NewPacket(payload, layers.LayerTypeDNS, gopacket.Default)
	if parsedDNS := packet.Layer(layers.LayerTypeDNS); parsedDNS != nil {
		isDNSQuery = !parsedDNS.(*layers.DNS).QR
	}
	return
}

type packetWriter interface {
	WritePacketData([]byte) error
}

func sendBonjourPacket(handle packetWriter, bonjourPacket *bonjourPacket, tag uint16, brMACAddress net.HardwareAddr) {
	*bonjourPacket.vlanTag = tag
	*bonjourPacket.srcMAC = brMACAddress

	// Network devices may set dstMAC to the local MAC address
	// Rewrite dstMAC to ensure that it is set to the appropriate multicast MAC address
	if bonjourPacket.isIPv6 {
		*bonjourPacket.dstMAC = net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0xFB}
	} else {
		*bonjourPacket.dstMAC = net.HardwareAddr{0x01, 0x00, 0x5E, 0x00, 0x00, 0xFB}
	}

	buf := gopacket.NewSerializeBuffer()
	gopacket.SerializePacket(buf, gopacket.SerializeOptions{}, bonjourPacket.packet)
	handle.WritePacketData(buf.Bytes())
}
