// SPDX-License-Identifier: Apache-2.0

package traffic

import "errors"

// MessageReadWriter is the message interface used by a datagram transport.
// It matches frp's message read/writer without depending on an frp version.
type MessageReadWriter[M any] interface {
	ReadMsg() (M, error)
	ReadMsgInto(M) error
	WriteMsg(M) error
}

// PayloadSize identifies UDP payload messages and returns the length of their
// decoded contents. It must return false for control and heartbeat messages.
// It can be called concurrently by the reader and writer.
type PayloadSize[M any] func(M) (int, bool)

// WrapDatagrams counts only complete payload messages accepted by the message
// reader/writer. Reads are downloads and writes are uploads, for both a proxy
// and a visitor. Failed message operations and control messages are not counted.
// Counts describe the payload crossing this boundary, not guaranteed delivery
// to the ultimate local or remote UDP socket.
//
// Wrap the message codec after negotiation and transport wrapping, never the
// raw work connection. For frp 0.72 the size function should identify only
// *msg.UDPPacket and return len(packet.Content). The same wrapper must not be
// installed twice on a forwarding path. A nil counter leaves rw untouched;
// otherwise size is required so missing instrumentation is not silently treated
// as zero traffic.
func WrapDatagrams[M any](rw MessageReadWriter[M], counter Counter, size PayloadSize[M]) (MessageReadWriter[M], error) {
	if counter == nil {
		return rw, nil
	}
	if size == nil {
		return nil, errors.New("traffic: UDP payload size function is required")
	}
	return &meteredMessages[M]{rw: rw, counter: counter, size: size}, nil
}

type meteredMessages[M any] struct {
	rw      MessageReadWriter[M]
	counter Counter
	size    PayloadSize[M]
}

func (m *meteredMessages[M]) ReadMsg() (M, error) {
	message, err := m.rw.ReadMsg()
	if err == nil {
		m.download(message)
	}
	return message, err
}

func (m *meteredMessages[M]) ReadMsgInto(message M) error {
	err := m.rw.ReadMsgInto(message)
	if err == nil {
		m.download(message)
	}
	return err
}

func (m *meteredMessages[M]) WriteMsg(message M) error {
	err := m.rw.WriteMsg(message)
	if err == nil {
		if n, ok := m.size(message); ok && n > 0 {
			m.counter.AddUpload(uint64(n))
		}
	}
	return err
}

func (m *meteredMessages[M]) download(message M) {
	if n, ok := m.size(message); ok && n > 0 {
		m.counter.AddDownload(uint64(n))
	}
}
