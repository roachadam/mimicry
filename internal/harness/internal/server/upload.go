package server

import (
	"strconv"
	"strings"
)

// HTTP1Upload has the browser upload bodies over HTTP/1.1 and the server
// keep every TLS record the client sends (capture.H1Upload): how each
// browser cuts a request, head and body, into records (issue #33).
var HTTP1Upload = &Scenario{
	Name:          "http1-upload",
	Doc:           "uploads over HTTP/1.1 (10 MiB by fetch() of an ArrayBuffer view, twice, a Blob and FormData; small bodies of many sizes; a stream; a 10 MiB form POST): the TLS records carrying each request",
	HTTP1:         true,
	recordUploads: true,
	respond:       uploadResponse(uploadScript, true),
}

// H2Upload is HTTP1Upload's first two uploads over HTTP/2, on the page's
// connection after its first few KB: whether a browser grows its records
// at a connection's start there (h2trace.Trace.Records holds every
// record).
var H2Upload = &Scenario{
	Name:          "h2-upload",
	Doc:           "two 10 MiB uploads by fetch() of an ArrayBuffer view over HTTP/2, on the page's connection: the TLS records carrying them",
	recordUploads: true,
	respond:       uploadResponse(h2UploadScript, false),
}

// UploadSize is each large upload's body, the scripts' 10 * 1024 * 1024:
// the benchmark's 10 MB (internal/bench).
const UploadSize = 10 << 20

// uploadScript uploads UploadSize bytes from an ArrayBuffer view on a new
// connection (every earlier response closed its connection), then again
// on the same one, from a Blob and as FormData with a file. Then small
// bodies: views of many sizes (the 1-byte steps cross Chrome's 1400-byte
// limit for a head and body in one record, the ones near 16 KiB a
// record's end), Blobs, URLSearchParams and FormData without a file.
// Then a ReadableStream body, which a browser may refuse over HTTP/1.1
// (the step's failure is ignored), and a form POST of the file, whose
// landing page asks for /done.
const uploadScript = `(async () => {
  const body = new Uint8Array(10 * 1024 * 1024);
  const post = async (path, b) => { await (await fetch(path, {method: 'POST', body: b})).text(); };
  await post('/up/buffer', body);
  await post('/up/buffer-again', body);
  await post('/up/blob', new Blob([body]));
  const data = new FormData();
  data.append('file', new File([body], 'upload.bin'));
  await post('/up/formdata', data);
  const sizes = [1, 100, 500, 700];
  for (let n = 740; n <= 880; n++) { sizes.push(n); }
  sizes.push(900, 1000, 1400, 2000, 4096, 8192, 15000, 15900, 15910, 16000, 16384, 16385, 32768, 65536);
  for (const n of sizes) { await post('/up/sweep-' + n, new Uint8Array(n)); }
  for (const n of [100, 500, 5000]) { await post('/up/blob-' + n, new Blob([new Uint8Array(n)])); }
  await post('/up/params', new URLSearchParams({user: 'mimicry', note: 'x'.repeat(200)}));
  const small = new FormData();
  small.append('note', 'x'.repeat(200));
  await post('/up/formdata-small', small);
  try {
    let n = 0;
    const stream = new ReadableStream({pull(c) { if (n++ < 4) { c.enqueue(new Uint8Array(65536)); } else { c.close(); } }});
    await (await fetch('/up/stream', {method: 'POST', body: stream, duplex: 'half'})).text();
  } catch (e) {}
  const input = document.createElement('input');
  input.type = 'file';
  input.name = 'file';
  const files = new DataTransfer();
  files.items.add(new File([body], 'upload.bin'));
  input.files = files.files;
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = '/up/form';
  form.enctype = 'multipart/form-data';
  form.append(input);
  document.body.append(form);
  form.submit();
})();
`

// h2UploadScript is uploadScript's first two uploads.
const h2UploadScript = `(async () => {
  const body = new Uint8Array(10 * 1024 * 1024);
  const post = async (path, b) => { await (await fetch(path, {method: 'POST', body: b})).text(); };
  await post('/up/buffer', body);
  await post('/up/buffer-again', body);
  await fetch('/done');
})();
`

// uploadLanding is the page the form's POST lands on.
const uploadLanding = `<!doctype html>
<html><head><meta charset="utf-8"><title>mimicry capture</title>
<link rel="icon" href="data:,">
</head><body>
<p>mimicry capture ok</p>
<script>fetch('/done');</script>
</body></html>
`

// uploadResponse answers an upload page whose app.js is script. With
// fresh, the page's other responses close their connections, so the
// first upload opens one of its own (HTTP/1.1 only: an HTTP/2 page
// capture follows one connection).
func uploadResponse(script string, fresh bool) func(path string, uploaded int) (response, bool) {
	return func(path string, uploaded int) (response, bool) {
		switch path {
		case "/", "/style.css", "/img.png":
			r := defaultResponse(path, 0)

			r.closeAfter = fresh

			return r, true

		case "/app.js":
			return response{
				status:      200,
				contentType: "text/javascript",
				body:        []byte(script),
				closeAfter:  fresh,
			}, true

		case "/up/form":
			return response{
				status:      200,
				contentType: "text/html; charset=utf-8",
				body:        []byte(uploadLanding),
			}, true
		}

		if strings.HasPrefix(path, "/up/") {
			return response{
				status:      200,
				contentType: "text/plain",
				body:        []byte(strconv.Itoa(uploaded) + "\n"),
			}, true
		}

		return response{}, false
	}
}
