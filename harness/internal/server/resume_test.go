package server

import (
	"crypto/tls"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/roachadam/mimicry/harness/internal/capture"
	"github.com/roachadam/mimicry/profile"
)

// The resume page runs every step from its document; each sequential
// step's response ends its connection, over HTTP/2 and HTTP/1.1, so a
// client holding a ticket resumes on the next; the page capture records
// each connection's ClientHello, its PSK offer and the server's answer.
func TestResume(t *testing.T) {
	client, port, captures := replayServer(t, Resume)
	tr := client.Transport.(*http.Transport)
	tr.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	url := func(host, path string) string {
		return "https://" + host + ":" + port + path
	}
	_, html := fetch(t, client, "GET", url(kindsHost, "/"), nil)
	for _, step := range ResumeSteps {
		for label := range strings.SplitSeq(step, "|") {
			if !strings.Contains(html, "/k/"+label+`"`) {
				t.Errorf("document lacks step %s", label)
			}
		}
	}
	for _, target := range []string{
		url(kindsHost, "/k/r1.so"),
		url(kindsHost, "/k/r2.so"),
		url(resumeH1, "/k/r1.h1"),
		url(resumeH1, "/k/r2.h1"),
	} {
		fetch(t, client, "GET", target, nil)
	}
	fetch(t, client, "GET", url(kindsHost, "/done"), nil)
	var page *capture.Page
	for page == nil {
		if c := receive(t, captures); c.Finished {
			page = c.Page
		}
	}
	got := make([]string, 0, len(page.Connections))
	for _, c := range page.Connections {
		state := c.Host + " " + c.Protocol
		if c.Resumed {
			state += " resumed"
		}
		if c.Accepted {
			state += " accepted"
		}
		got = append(got, state)
		if c.Hello == nil || len(c.Hello.Extensions) == 0 {
			t.Errorf("%s: no ClientHello recorded", state)
		}
		if c.Resumed != (c.PSKIdentities == 1 && c.PSKIdentity != "") {
			t.Errorf("%s: %d identities %q", state, c.PSKIdentities, c.PSKIdentity)
		}
		if hasPSK := slices.ContainsFunc(c.Hello.Extensions, func(e profile.Extension) bool {
			return e.Type == profile.ExtPreSharedKey
		}); c.Resumed != hasPSK {
			t.Errorf("%s: hello and resumption disagree", state)
		}
	}
	want := []string{
		kindsHost + " h2",
		kindsHost + " h2 resumed accepted",
		kindsHost + " h2 resumed accepted",
		resumeH1 + " http/1.1",
		resumeH1 + " http/1.1 resumed accepted",
		kindsHost + " h2 resumed accepted",
	}
	if !slices.Equal(got, want) {
		t.Errorf("connections:\n got %q\nwant %q", got, want)
	}
}
