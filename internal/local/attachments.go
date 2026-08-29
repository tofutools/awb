package local

import (
	"context"
	"io"

	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
	"github.com/tofutools/awb/internal/storage"
)

// AddAttachment stores a file against an issue.
//
// The content is copied into the store first, outside the transaction, because
// that is the slow half and holding SQLite's write lock across a large upload
// would stop every other writer for as long as it took. What happens inside
// the transaction is the cheap half: the row, and the rename that gives the
// content its final name.
//
// Placing the content while holding the write lock is what orders an upload
// against a concurrent delete of the same content, whose own unlink holds that
// lock too — see sweep. Naming a blob by its own digest is what makes the
// rename idempotent.
func (b *Backend) AddAttachment(ctx context.Context, issueRef string,
	req backend.AttachmentCreate) (*domain.Attachment, error) {
	name, err := domain.ValidateAttachmentName(req.Name)
	if err != nil {
		return nil, err
	}
	contentType := ""
	if req.ContentType != "" {
		if contentType, err = domain.ValidateContentType(req.ContentType); err != nil {
			return nil, err
		}
	}

	// An early refusal, so that a mistyped issue reference does not first copy
	// a large file onto the disk to throw it away. It is not the check: the
	// reference is resolved again inside the transaction below, which is the
	// only place the answer is good at the moment it is used.
	if err := b.db.Read(ctx, func(tx *storage.Tx) error {
		_, err := resolve(tx, issueRef)
		return err
	}); err != nil {
		return nil, err
	}

	staged, err := b.blobs.Stage(req.Content, domain.MaxAttachmentBytes)
	if err != nil {
		return nil, err
	}
	// Whatever happens after this, the staged file is either renamed into place
	// or removed; it is never left behind under its temporary name.
	placed := false
	defer func() {
		if !placed {
			b.blobs.Discard(staged)
		}
	}()

	if contentType == "" {
		contentType = domain.DetectContentType(staged.Head)
	}

	attachment := &domain.Attachment{
		Name:        name,
		ContentType: contentType,
		Size:        staged.Size,
		Sha256:      staged.Sha256,
	}
	err = b.write(ctx, func(tx *storage.Tx) error {
		issueID, err := resolve(tx, issueRef)
		if err != nil {
			return err
		}
		attachment.Issue = issueID

		// The row goes in first: if it fails the transaction rolls back and
		// nothing has been written to the store either. The content is placed
		// before the commit, so a committed row never names a file that is not
		// there.
		if err := tx.InsertAttachment(attachment); err != nil {
			return err
		}
		if err := b.blobs.Place(staged); err != nil {
			return err
		}
		placed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attachment, nil
}

// GetAttachment reads one attachment's metadata.
func (b *Backend) GetAttachment(ctx context.Context, ref string) (*domain.Attachment, error) {
	var attachment *domain.Attachment
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		var err error
		attachment, err = loadAttachment(tx, ref)
		return err
	})
	if err != nil {
		return nil, err
	}
	return attachment, nil
}

// ListAttachments lists one issue's attachments.
func (b *Backend) ListAttachments(ctx context.Context, issueRef string,
	limit, offset *int) (backend.AttachmentPage, error) {
	var page backend.AttachmentPage
	err := b.db.Read(ctx, func(tx *storage.Tx) error {
		issueID, err := resolve(tx, issueRef)
		if err != nil {
			return err
		}
		page.Attachments, page.Total, err = tx.ListAttachments(issueID, limit, offset)
		return err
	})
	if err != nil {
		return backend.AttachmentPage{}, err
	}
	return page, nil
}

// OpenAttachment reads one attachment's metadata and opens its content. The
// reader is the caller's to close.
func (b *Backend) OpenAttachment(ctx context.Context, ref string) (
	*domain.Attachment, io.ReadCloser, error) {
	attachment, err := b.GetAttachment(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	content, err := b.blobs.Open(attachment.Sha256)
	if err != nil {
		return nil, nil, err
	}
	return attachment, content, nil
}

// DeleteAttachment removes an attachment and, when no other attachment holds
// the same content, the file behind it.
func (b *Backend) DeleteAttachment(ctx context.Context, ref string) (*domain.Attachment, error) {
	var deleted *domain.Attachment
	err := b.write(ctx, func(tx *storage.Tx) error {
		attachment, err := loadAttachment(tx, ref)
		if err != nil {
			return err
		}
		if err := tx.DeleteAttachment(attachment.ID); err != nil {
			return err
		}
		deleted = attachment
		return nil
	})
	if err != nil {
		return nil, err
	}
	b.sweep(ctx, []string{deleted.Sha256})
	return deleted, nil
}

// loadAttachment resolves a reference and reads the attachment behind it.
func loadAttachment(tx *storage.Tx, ref string) (*domain.Attachment, error) {
	parsed, err := domain.ParseAttachmentRef(ref)
	if err != nil {
		return nil, err
	}
	id, err := tx.ResolveAttachmentRef(parsed)
	if err != nil {
		return nil, err
	}
	return tx.GetAttachment(id)
}

// sweep removes the content behind each digest that no attachment row names
// any more. It is what every delete calls once its rows are gone.
//
// It runs *after* the deleting transaction has committed, and never inside it.
// A filesystem unlink cannot be rolled back, so one performed before the
// commit and followed by a failure — an I/O error, a cancelled context — would
// leave the restored rows naming a file that is no longer there, which is the
// one state this design does not tolerate. Afterwards, the same failure leaves
// an unreferenced file instead: unreachable, harmless, and adopted by the next
// upload of the same bytes.
//
// It still takes the write lock, because that is what orders the unlink
// against a concurrent upload of the same bytes: an upload places its content
// inside its own transaction, so the count here and the unlink it guards
// cannot be split by one. The transaction writes nothing, so its own commit
// has nothing to lose.
//
// A failure to remove a file is not reported. It leaves exactly the
// unreferenced file the design already tolerates, and the deletion the caller
// asked for has already happened — failing it afterwards would report
// something that is not true.
func (b *Backend) sweep(ctx context.Context, digests []string) {
	if len(digests) == 0 {
		return
	}
	_ = b.write(ctx, func(tx *storage.Tx) error {
		for _, sum := range digests {
			unreferenced, err := tx.DigestIsUnreferenced(sum)
			if err != nil {
				return err
			}
			if !unreferenced {
				continue
			}
			if err := b.blobs.Remove(sum); err != nil {
				return err
			}
		}
		return nil
	})
}
