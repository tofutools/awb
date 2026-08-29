package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tofutools/awb/internal/awberr"
)

// Blobs is the attachment content store: one directory of files, each named by
// the lowercase hexadecimal SHA-256 of what is in it.
//
// The content of an attachment is deliberately not in the database. It sits
// beside it by default and may be pointed at a filesystem of its own, so a
// tracker holding large files stays a small database file that can be copied,
// backed up and read with a SQLite shell.
//
// Naming a file by its own digest is what makes writing one idempotent: the
// bytes are already there or they are not, and two attachments holding the
// same content share one file. It is also what lets the content be written
// before the row that names it, so a committed row never points at a file that
// is not there. The reverse — a file no row names — is the failure that is left
// behind by an interrupted upload, and it is harmless: it is unreachable, and
// the next upload of the same bytes adopts it.
type Blobs struct {
	dir string
}

// stagedPrefix marks the temporary files an upload writes before it knows what
// to call them. It cannot collide with a digest, which is hexadecimal.
const stagedPrefix = "staging-"

// NewBlobs names the directory without touching it. Nothing is created until
// something is stored, so a read-only command against a tracker that has no
// attachments never creates a directory for them.
func NewBlobs(dir string) *Blobs { return &Blobs{dir: dir} }

// Dir is the directory the content is stored in.
func (b *Blobs) Dir() string { return b.dir }

// Create makes the directory, which is what awb init does so that the layout
// exists before anything is written into it.
func (b *Blobs) Create() error {
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "create attachment directory %s", b.dir)
	}
	return nil
}

// Staged is content written to the store but not yet named: the temporary file
// it went into, its size and the digest that is its final name.
type Staged struct {
	Path   string
	Size   int64
	Sha256 string
	// Head is the first bytes of the content, which is what the content type is
	// sniffed from when the caller states none.
	Head []byte
}

// headBytes is how much of the content the sniffing rule looks at. It is what
// net/http's own detector reads.
const headBytes = 512

// Stage copies content into a temporary file in the store, hashing it as it
// goes, and reports what it would be called. It is the slow half of an upload
// and is deliberately outside the write transaction; Place, which is fast,
// is what happens inside one.
//
// It refuses content over maxBytes rather than truncating it, reading one byte
// past the limit to tell "exactly the maximum" from "more than it".
func (b *Blobs) Stage(content io.Reader, maxBytes int64) (*Staged, error) {
	if err := b.Create(); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(b.dir, stagedPrefix+"*")
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "write to attachment directory %s", b.dir)
	}
	path := file.Name()

	staged, err := b.fill(file, content, maxBytes)
	closeErr := file.Close()
	if err == nil && closeErr != nil {
		err = awberr.Wrap(awberr.Runtime, closeErr, "write %s", path)
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	staged.Path = path
	return staged, nil
}

func (b *Blobs) fill(file *os.File, content io.Reader, maxBytes int64) (*Staged, error) {
	digest := sha256.New()
	head := make([]byte, 0, headBytes)

	// One byte past the limit, so a file of exactly maxBytes is accepted and the
	// first byte beyond it is what the refusal notices.
	limited := io.LimitReader(content, maxBytes+1)
	buf := make([]byte, 64*1024)
	var size int64
	for {
		n, err := limited.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			size += int64(n)
			if size > maxBytes {
				return nil, awberr.Usagef(
					"attachment is too large: more than %d bytes, which is the maximum", maxBytes)
			}
			if len(head) < headBytes {
				head = append(head, chunk[:min(len(chunk), headBytes-len(head))]...)
			}
			digest.Write(chunk) //nolint:errcheck // hash.Hash never fails
			if _, werr := file.Write(chunk); werr != nil {
				return nil, awberr.Wrap(awberr.Runtime, werr, "write %s", file.Name())
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, awberr.Wrap(awberr.Runtime, err, "read the attachment content")
		}
	}

	return &Staged{
		Size:   size,
		Sha256: hex.EncodeToString(digest.Sum(nil)),
		Head:   head,
	}, nil
}

// Place gives staged content its final name.
//
// It is called inside the write transaction, while that writer holds SQLite's
// exclusive turn, which is what keeps it ordered against the unlink a delete
// performs: without that, a delete that had just found the last row gone could
// remove the file an upload had already written.
//
// Renaming over an existing file is not a mistake to report: the name is the
// digest, so whatever is there holds the same bytes.
func (b *Blobs) Place(staged *Staged) error {
	if err := os.Rename(staged.Path, b.pathOf(staged.Sha256)); err != nil {
		return awberr.Wrap(awberr.Runtime, err, "store the attachment content")
	}
	return nil
}

// Discard removes staged content an upload did not go on to store. A failure
// leaves an unreachable file behind and is not worth failing the command over.
func (b *Blobs) Discard(staged *Staged) {
	if staged != nil && staged.Path != "" {
		_ = os.Remove(staged.Path)
	}
}

// Open reads stored content. A digest with no file behind it is a runtime
// failure naming the path: the row promised content that the directory does
// not hold, which means the wrong directory or a missing file rather than
// anything the caller did wrong.
func (b *Blobs) Open(sum string) (io.ReadCloser, error) {
	file, err := os.Open(b.pathOf(sum))
	if errors.Is(err, os.ErrNotExist) {
		return nil, awberr.Runtimef(
			"the content of this attachment is missing from %s: expected the file %s",
			b.dir, sum)
	}
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "read the attachment content")
	}
	return file, nil
}

// Remove deletes stored content. A file that is already gone is not a failure:
// what the caller asked for is that it not be there.
func (b *Blobs) Remove(sum string) error {
	err := os.Remove(b.pathOf(sum))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return awberr.Wrap(awberr.Runtime, err, "remove the attachment content %s", sum)
	}
	return nil
}

// pathOf is the file one digest is stored in. The digest is validated before
// it is ever stored, so it is 64 hexadecimal characters and can name nothing
// but a file directly in this directory.
func (b *Blobs) pathOf(sum string) string { return filepath.Join(b.dir, sum) }
