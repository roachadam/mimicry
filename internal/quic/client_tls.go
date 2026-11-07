package quic

// mimicry: a client connection's TLS on the uTLS fork with the profile's
// ClientHello, its sessions and 0-RTT. Not in upstream; see UPSTREAM.md.

import (
	"bytes"
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

	// The spec is read only here; release it for the connection's lifetime.
	c.client.hello = nil
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

		if cc.rememberApp {
			cc.recalled, _ = recallApp(s.Extra)
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

		if cc.rememberApp && s.EarlyData {
			if cc.appState == nil {
				// Held until SetApplicationState: a 0-RTT ticket
				// without the SETTINGS would resume with no table.
				cc.pending = append(cc.pending, s)

				return true, nil
			}

			s.Extra = rememberedApp(s.Extra, cc.appState)
		}

		return true, c.storeSession(s)

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

	default:
		// The rest are upstream's (handleTLSEvents).
	}

	return false, nil
}

// storeSession hands s to uTLS's session cache.
func (c *Conn) storeSession(s *tls.SessionState) error {
	if u, ok := c.tls.(*tls.UQUICConn); ok {
		return u.StoreSession(s)
	}

	return nil
}

// rememberedAppPrefix marks the application's state (HTTP/3: the server's
// SETTINGS) in a ticket's SessionState.Extra.
var rememberedAppPrefix = []byte("mimicry/application-state\x00")

// rememberedApp appends the application's state to a ticket's Extra.
func rememberedApp(extra [][]byte, state []byte) [][]byte {
	return append(extra, append(append([]byte(nil), rememberedAppPrefix...), state...))
}

// recallApp finds the application's state in a ticket's Extra.
func recallApp(extra [][]byte) ([]byte, bool) {
	for _, e := range extra {
		if body, ok := bytes.CutPrefix(e, rememberedAppPrefix); ok {
			return body, true
		}
	}

	return nil, false
}

// SetApplicationState gives the application's state to store with the
// connection's 0-RTT tickets (DialConfig.RememberApplication): those held
// for it are stored now, later ones with it. Only the first call counts:
// the server's SETTINGS come once.
func (c *Conn) SetApplicationState(state []byte) {
	if c.client == nil || !c.client.rememberApp {
		return
	}

	state = cloneBytes(state)

	_ = c.runOnLoop(context.Background(), func(now time.Time, c *Conn) {
		cc := c.client
		if cc.appState != nil {
			return
		}

		cc.appState = state

		for _, s := range cc.pending {
			s.Extra = rememberedApp(s.Extra, state)
			if err := c.storeSession(s); err != nil {
				c.abort(now, err)

				return
			}
		}

		cc.pending = nil
	})
}

// RememberedApplicationState is the application's state stored with the
// ticket the connection resumed with 0-RTT (DialConfig.RememberApplication),
// or nil: none, or no 0-RTT. Set before Dial returns.
func (c *Conn) RememberedApplicationState() []byte {
	if c.client == nil {
		return nil
	}

	return c.client.recalled
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
	// Under tableMu (conn_streams.go): a stream opened after this read
	// the new windows; one opened before is in the table.
	c.streams.tableMu.Lock()
	defer c.streams.tableMu.Unlock()

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
	c.streams.tableMu.Lock() // see tableMu (conn_streams.go)
	defer c.streams.tableMu.Unlock()

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
