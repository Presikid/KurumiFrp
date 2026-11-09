// SPDX-License-Identifier: Apache-2.0

package traffic

import "github.com/fatedier/frp/pkg/msg"

// WrapUDPPackets measures decoded UDP payload messages, excluding heartbeats,
// address metadata and framing. It must be installed once per message codec.
func WrapUDPPackets(rw msg.ReadWriter, counter Counter) msg.ReadWriter {
	wrapped, _ := WrapDatagrams[msg.Message](rw, counter, func(m msg.Message) (int, bool) {
		switch packet := m.(type) {
		case *msg.UDPPacket:
			if packet != nil {
				return len(packet.Content), true
			}
		case msg.UDPPacket:
			return len(packet.Content), true
		}
		return 0, false
	})
	return wrapped
}
