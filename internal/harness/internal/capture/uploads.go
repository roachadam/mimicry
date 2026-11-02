package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/roachadam/mimicry/profile"
)

// UploadScenario is the scenario whose captures hold how a browser cuts
// HTTP/1.1 requests with a body into TLS records (server.HTTP1Upload).
const UploadScenario = "http1-upload"

// Cuts of one request into records (H1Upload.Cut).
const (
	// CutOne: the whole request in one record.
	CutOne = "one"
	// CutApart: the head in a record of its own, the body from a new
	// one, full records but the last of each.
	CutApart = "apart"
	// CutStream: head and body one stream, every record full but the
	// last.
	CutStream = "stream"
)

// Cut says how the client cut the request with a body into records:
// CutOne, CutApart or CutStream. Any other cut is an error: a record
// shared with another request, a record short of full before the head's
// or the body's end.
func (u *H1Upload) Cut() (string, error) {
	if u.Lead != 0 || u.Trail != 0 {
		return "", fmt.Errorf("a record carries %d bytes before the request and %d after it", u.Lead, u.Trail)
	}

	var sizes []int
	for _, r := range u.Records {
		for range r.Count {
			sizes = append(sizes, r.Size)
		}
	}

	full := func(records []int) bool {
		for _, n := range records[:len(records)-1] {
			if n != profile.MaxRecord {
				return false
			}
		}

		return len(records) > 0 && records[len(records)-1] > 0
	}

	head := 0
	for i, n := range sizes {
		if head+n >= u.Head {
			if head+n == u.Head && i+1 < len(sizes) && full(sizes[:i+1]) && full(sizes[i+1:]) {
				return CutApart, nil
			}

			break
		}

		head += n
	}

	switch {
	case len(sizes) == 1:
		return CutOne, nil
	case full(sizes):
		return CutStream, nil
	}

	runs := make([]string, len(u.Records))
	for i, r := range u.Records {
		runs[i] = fmt.Sprintf("%dx%d", r.Size, r.Count)
	}

	return "", fmt.Errorf("records %s fit no cut of a %d-byte head and %d-byte body", strings.Join(runs, " "), u.Head, u.Body)
}

// FromFile reports whether the upload page (server.HTTP1Upload) sent the
// request at path with a Blob or File body, which Chrome never joins with
// its head (its body is not in memory) where the same bytes from an
// ArrayBuffer view would be: such requests decide how the head goes
// (apart or one stream) but not UploadRecords.JoinUpTo.
func FromFile(path string) bool {
	return path == "/up/blob" || strings.HasPrefix(path, "/up/blob-") || path == "/up/formdata" || path == "/up/form"
}

// UploadFiles lists the capture files UploadRecords reads for p: the
// http1-upload capture of the profile p takes it from (Source), and that
// profile's testing-build capture (<name>.http1-upload.chrome-for-testing
// .h1trace.json.gz, decision 0042: the record cut is the network stack's,
// which a testing build shares with the brand), those that exist.
func UploadFiles(dir string, p *profile.Profile) ([]string, error) {
	source := Source(p, UploadScenario)

	var out []string

	for _, name := range []string{
		source + "." + UploadScenario + ".h1trace.json.gz",
		source + "." + UploadScenario + forTesting + ".h1trace.json.gz",
	} {
		path := filepath.Join(dir, name)

		_, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		out = append(out, path)
	}

	return out, nil
}

// UploadRecords is how p's browser cuts an HTTP/1.1 request with a body
// into TLS records (profile http1.uploadRecords), from every request of
// p's upload captures (UploadFiles): the head apart or one stream, as
// every request says, and the largest request sent whole with its head
// in one record, which the next byte up must have split (the samples
// measure it, not bracket it). Requests with a Blob or File body
// (FromFile) count for the first only. Nil when no capture measured it.
func UploadRecords(dir string, p *profile.Profile) (*profile.UploadRecords, error) {
	files, err := UploadFiles(dir, p)
	if err != nil || len(files) == 0 {
		return nil, err
	}

	var (
		apart, stream     string
		joined, split, at string
		joinedTotal       int
		splitTotal        = -1
	)

	for _, path := range files {
		data, err := ReadTrace(path)
		if err != nil {
			return nil, err
		}

		var samples [][]H1Request
		if err := json.Unmarshal(data, &samples); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		for i, sample := range samples {
			for _, r := range sample {
				if r.Upload == nil || r.Upload.Body == 0 {
					continue
				}

				at = fmt.Sprintf("%s: sample %d, %s", path, i+1, r.Line)

				cut, err := r.Upload.Cut()
				if err != nil {
					return nil, fmt.Errorf("%s: %w", at, err)
				}

				total := r.Upload.Head + r.Upload.Body

				switch cut {
				case CutApart:
					apart = at
				case CutStream:
					stream = at
				}

				_, target, _ := strings.Cut(r.Line, " ")

				target, _, _ = strings.Cut(target, " ")

				if FromFile(target) {
					continue
				}

				switch {
				case cut == CutOne && total > joinedTotal:
					joinedTotal, joined = total, at
				case cut != CutOne && (splitTotal < 0 || total < splitTotal):
					splitTotal, split = total, at
				}
			}
		}
	}

	switch {
	case apart != "" && stream != "":
		return nil, fmt.Errorf("the head goes apart (%s) and in one stream with the body (%s)", apart, stream)

	case stream != "":
		// A stream is one record up to a record's size.
		if joinedTotal > profile.MaxRecord || (splitTotal >= 0 && splitTotal <= profile.MaxRecord) {
			return nil, fmt.Errorf("one stream, but %d bytes went whole (%s) and %d not (%s)", joinedTotal, joined, splitTotal, split)
		}

		return &profile.UploadRecords{}, nil

	case apart == "":
		return nil, fmt.Errorf("%s: no request needed more than one record", files[0])
	}

	switch {
	case joinedTotal == 0:
		return &profile.UploadRecords{
			HeadApart: true,
		}, nil

	case joinedTotal >= splitTotal:
		return nil, fmt.Errorf("%d bytes went whole (%s), %d apart (%s)", joinedTotal, joined, splitTotal, split)

	case joinedTotal+1 != splitTotal:
		return nil, fmt.Errorf("the join limit is between %d bytes (%s) and %d (%s), not measured", joinedTotal, joined, splitTotal, split)
	}

	return &profile.UploadRecords{
		HeadApart: true,
		JoinUpTo:  joinedTotal,
	}, nil
}
