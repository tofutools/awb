package handler

import (
	"context"
	"mime"

	"github.com/tofutools/awb/internal/api"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// AddAttachment stores the request body as a file against an issue.
//
// The body is handed to the backend as the stream it arrived as, so an upload
// is never held in memory whole. Its size is bounded by the transport cap the
// server puts on this one endpoint, which is the same number the domain layer
// enforces, so the two surfaces refuse the same file.
func (h *Handler) AddAttachment(ctx context.Context, req api.AddAttachmentReq,
	params api.AddAttachmentParams) (*api.AttachmentCreatedHeaders, error) {
	attachment, err := h.backendFor(ctx).AddAttachment(ctx, params.ID, backend.AttachmentCreate{
		Name:        string(params.Name),
		ContentType: string(params.ContentType.Or("")),
		Content:     req.Data,
	})
	if err != nil {
		return nil, err
	}
	return &api.AttachmentCreatedHeaders{
		Location: api.NewOptString("/api/attachments/" + attachment.ID),
		Response: toAttachment(attachment),
	}, nil
}

func (h *Handler) GetAttachment(ctx context.Context, params api.GetAttachmentParams) (
	*api.Attachment, error) {
	attachment, err := h.backendFor(ctx).GetAttachment(ctx, params.Aid)
	if err != nil {
		return nil, err
	}
	converted := toAttachment(attachment)
	return &converted, nil
}

func (h *Handler) ListAttachments(ctx context.Context, params api.ListAttachmentsParams) (
	*api.AttachmentListHeaders, error) {
	page, err := h.backendFor(ctx).ListAttachments(ctx, params.ID,
		optInt(params.Limit), optInt(params.Offset))
	if err != nil {
		return nil, err
	}
	return &api.AttachmentListHeaders{
		XTotalCount: api.NewOptInt(page.Total),
		Response:    toAttachments(page.Attachments),
	}, nil
}

// GetAttachmentContent answers with the bytes as they were uploaded.
//
// They are always served as application/octet-stream, which is what the
// document declares and therefore what the generated encoder sends: uploaded
// content comes back from the same origin as the UI, and a browser invited to
// render it there would run whatever an uploaded HTML file said. The stored
// content type stays metadata, where nothing acts on it.
//
// Content-Length is the recorded size rather than one measured on the way
// past, so a stored file that no longer matches its metadata breaks the
// transfer instead of arriving as a plausible short one. It can be sent at all
// only because this is the response serve does not compress; see gzipExcept.
//
// The reader is closed by the generated encoder once the body has been
// written.
func (h *Handler) GetAttachmentContent(ctx context.Context,
	params api.GetAttachmentContentParams) (*api.GetAttachmentContentOKHeaders, error) {
	attachment, content, err := h.backendFor(ctx).OpenAttachment(ctx, params.Aid)
	if err != nil {
		return nil, err
	}
	return &api.GetAttachmentContentOKHeaders{
		ContentDisposition: api.NewOptString(contentDisposition(attachment.Name)),
		ContentLength:      api.NewOptInt(int(attachment.Size)),
		Response:           api.GetAttachmentContentOK{Data: content},
	}, nil
}

// DeleteAttachment answers with the attachment as it was immediately before
// deletion. It carries no ETag, an attachment having never had one.
func (h *Handler) DeleteAttachment(ctx context.Context, params api.DeleteAttachmentParams) (
	*api.Attachment, error) {
	deleted, err := h.backendFor(ctx).DeleteAttachment(ctx, params.Aid)
	if err != nil {
		return nil, err
	}
	converted := toAttachment(deleted)
	return &converted, nil
}

// contentDisposition names the download, letting mime do the quoting and the
// RFC 2231 encoding a name outside ASCII needs. A name it cannot encode at all
// yields the bare disposition rather than a malformed header: the download
// still works, and the browser falls back to the URL for a name.
func contentDisposition(name string) string {
	formatted := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if formatted == "" {
		return "attachment"
	}
	return formatted
}

func toAttachment(a *domain.Attachment) api.Attachment {
	return api.Attachment{
		ID:          a.ID,
		Issue:       a.Issue,
		Name:        api.AttachmentName(a.Name),
		ContentType: api.ContentType(a.ContentType),
		Size:        int(a.Size),
		SHA256:      a.Sha256,
		CreatedAt:   api.Timestamp(a.CreatedAt),
	}
}

func toAttachments(attachments []domain.Attachment) []api.Attachment {
	out := make([]api.Attachment, len(attachments))
	for i := range attachments {
		out[i] = toAttachment(&attachments[i])
	}
	return out
}
