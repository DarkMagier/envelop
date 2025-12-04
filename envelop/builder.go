package envelop

import "envelop/peer"

// Builder：链式构建 Envelope
type Builder struct {
	e Envelope
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) Version(v uint8) *Builder {
	b.e.Version = v
	return b
}

func (b *Builder) Flags(f uint8) *Builder {
	b.e.Flags = f
	return b
}

func (b *Builder) TTL(ttl uint8) *Builder {
	b.e.TTL = ttl
	return b
}

func (b *Builder) Dest(id peer.PeerID) *Builder {
	b.e.DestPeerID = id
	return b
}

func (b *Builder) Return(id peer.PeerID) *Builder {
	b.e.ReturnPeerID = id
	return b
}

func (b *Builder) Payload(p []byte) *Builder {
	b.e.InnerPayload = p
	b.e.InnerLen = uint16(len(p))
	return b
}

func (b *Builder) Build() (*Envelope, error) {
	return &b.e, nil
}
