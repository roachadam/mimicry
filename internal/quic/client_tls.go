package quic

// mimicry: a client connection's TLS on the uTLS fork with the profile's
// ClientHello, its sessions and 0-RTT. Not in upstream; see UPSTREAM.md.

import (
	"context"
	"errors"
	"time"

	tls "github.com/roachadam/utls"
)

// startClientTLS starts the handshake with the profile's ClientHello and
// transport parameters. A ticket allowing 0-RTT sets the 0-RTT keys here,
// before anything is sent.
func (c *Conn) startClientTLS(now time.Time, initialConnID []byte) error {
	c.keysInitial = initialKeysVersion(initialConnID, c.side, c.client.version)
	c.client.initialCID = initialConnID
	qconfig := &tls.QUICConfig{
		TLSConfig: c.config.TLSConfig,
		// The connection stores tickets itself, with the server's
		// transport parameters for 0-RTT.
		EnableSessionEvents: true,
	}
	u := tls.UQUICClient(qconfig, tls.HelloCustom)
	if err := u.ApplyPreset(c.client.hello); err != nil {
		return err
	}
	c.tls = u
	u.SetTransportParameters(c.client.params)
	if err := u.Start(context.TODO()); err != nil {
		return err
	}
	return c.handleTLSEvents(now)
}

// handleClientTLSEvent handles the events of a client connection with a
// profile that upstream's handleTLSEvents does not: 0-RTT keys, sessions,
// rejected early data. It reports whether it handled e.
func (c *Conn) handleClientTLSEvent(_ time.Time, e tls.QUICEvent) (bool, error) {
	cc := c.client
	switch e.Kind {
	case tls.QUICSetWriteSecret:
		if e.Level != tls.QUICEncryptionLevelEarly {
			return false, nil
		}
		if err := checkCipherSuite(e.Suite); err != nil {
			return true, err
		}
		cc.keys0RTT.init(e.Suite, e.Data)
		cc.early = true
		cc.earlyOK.Store(true)
		return true, nil
	case tls.QUICResumeSession:
		cc.resumeOffered = true
		s := e.SessionState
		if !s.EarlyData {
			return true, nil
		}
		p, ok := recallParams(s.Extra)
		if !ok || !cc.p.ZeroRTT {
			s.EarlyData = false
			return true, nil
		}
		return true, c.applyRememberedParams(p)
	case tls.QUICStoreSession:
		s := e.SessionState
		if cc.version != quicVersion1 {
			// The ticket belongs to v2 (RFC 9369, section 5): a
			// connection resuming it starts in v2 (TicketVersion).
			s.Extra = rememberedVersion(s.Extra, cc.version)
		}
		if s.EarlyData && cc.peerParams != nil {
			s.Extra = rememberedParams(s.Extra, cc.peerParams)
		}
		if u, ok := c.tls.(*tls.UQUICConn); ok {
			return true, u.StoreSession(s)
		}
		return true, nil
	case tls.QUICRejectedEarlyData:
		// Every 0-RTT packet is lost: their data goes again in 1-RTT
		// (RFC 9001, section 4.6.2).
		cc.earlyOK.Store(false)
		cc.keys0RTT = fixedKeys{}
		c.loss.discardPackets(appDataSpace, c.log, c.handleAckOrLoss)
		c.clientResetFlowControl()
		return true, nil
	case tls.QUICTransportParameters:
		cc.peerParams = cloneBytes(e.Data)
		return false, nil
	case tls.QUICWriteData:
		if e.Level == tls.QUICEncryptionLevelHandshake {
			// The client's Finished. After a full handshake, the browser
			// acknowledged the server's flight first (Firefox: it
			// verifies the certificate meanwhile).
			cc.ackFirst = cc.p.AckBeforeFinished && !cc.resumeOffered
		}
		return false, nil
	case tls.QUICSetReadSecret:
		if e.Level == tls.QUICEncryptionLevelEarly {
			return true, errors.New("quic: 0-RTT read keys on a client")
		}
		return false, nil
	case tls.QUICHandshakeDone:
		cc.handshakeComplete = true
		if cc.earlyOK.Load() {
			// 1-RTT keys replace 0-RTT ones (RFC 9001, section 4.9.3).
			cc.keys0RTT = fixedKeys{}
		}
		return false, nil
	}
	return false, nil
}

// handleNewToken passes a NEW_TOKEN frame's token to the application.
func (c *Conn) handleNewToken(token []byte) {
	// A v2 connection's tokens belong to v2 (RFC 9369, section 5).
	if c.client == nil || c.client.onToken == nil || !c.client.p.Tokens || c.client.version != quicVersion1 {
		return
	}
	c.client.onToken(cloneBytes(token))
}

// clientApplyPeerWindows gives streams opened before the server's transport
// parameters arrived (in 0-RTT, on the remembered ones) the server's stream
// windows, when larger: the remembered values hold only until the new ones
// arrive (RFC 9000, section 7.4.1). New streams read them when opened.
func (c *Conn) clientApplyPeerWindows(p transportParameters) {
	c.client.peerMaxData = p.initialMaxData
	for id, ms := range c.streams.streams {
		if ms.s == nil || id.initiator() != c.side {
			continue
		}
		_ = ms.s.handleMaxStreamData(c.streams.peerInitialMaxStreamDataRemote[id.streamType()])
	}
}

// clientResetFlowControl starts the send side of flow control again after
// the server rejected 0-RTT: none of the 0-RTT data counts, and what is
// sent again in 1-RTT is limited by the server's new transport parameters
// alone (RFC 9001, section 4.6.2). After a HelloRetryRequest the windows
// are zero until those parameters arrive.
func (c *Conn) clientResetFlowControl() {
	cc := c.client
	if cc.peerParams == nil {
		cc.peerMaxData = 0
		c.streams.peerInitialMaxStreamDataBidiLocal = 0
		c.streams.peerInitialMaxStreamDataRemote = [streamTypeCount]int64{}
	}
	c.streams.outflow.max = cc.peerMaxData
	c.streams.outflow.used = 0
	for id, ms := range c.streams.streams {
		if ms.s == nil || id.initiator() != c.side {
			continue
		}
		ms.s.resetSendWindow(c.streams.peerInitialMaxStreamDataRemote[id.streamType()])
	}
}

// resetSendWindow makes everything the stream has flushed unsent, within a
// new send window win (clientResetFlowControl). Nothing it sent was
// acknowledged: every 0-RTT packet was lost.
func (s *Stream) resetSendWindow(win int64) {
	s.outgate.lock()
	defer s.outUnlock()
	s.outmaxsent = 0
	s.outwin = win
	s.outunsent = rangeset[int64]{}
	if end := min(s.outflushed, win); end > s.out.start {
		s.outunsent.add(s.out.start, end)
	}
	if s.out.end > s.outwin {
		s.outblocked.setUnsent()
	} else {
		s.outblocked.clear()
	}
}
