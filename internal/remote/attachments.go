package remote

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tofutools/awb/internal/awberr"
	"github.com/tofutools/awb/internal/backend"
	"github.com/tofutools/awb/internal/domain"
)

// AddAttachment uploads a file, streaming the content as the request body.
//
// Everything about the file that is not its content travels as a query
// parameter, which is what leaves Content-Type free to describe the body on
// the wire and makes the API's way of saying what a file is the same as the
// command line's.
func (b *Backend) AddAttachment(ctx context.Context, issueRef string,
	req backend.AttachmentCreate) (*domain.Attachment, error) {
	query := url.Values{"name": {req.Name}}
	if req.ContentType != "" {
		query.Set("content-type", req.ContentType)
	}
	endpoint := b.endpoint("/api/issues/"+url.PathEscape(issueRef)+"/attachments", query)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, req.Content)
	if err != nil {
		return nil, awberr.Wrap(awberr.Runtime, err, "build request")
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	b.authenticate(request)

	resp, err := b.contentClient.Do(request)
	if err != nil {
		return nil, awberr.Runtimef("cannot reach %s: %s", b.base.Host, err.Error())
	}
	defer resp.Body.Close() //nolint:errcheck // the response is being discarded

	if resp.StatusCode >= 400 {
		return nil, b.apiError(resp)
	}
	var attachment domain.Attachment
	if err := decodeJSON(resp.Body, &attachment); err != nil {
		return nil, err
	}
	return &attachment, nil
}

func (b *Backend) GetAttachment(ctx context.Context, issueRef, name string) (
	*domain.Attachment, error) {
	var attachment domain.Attachment
	_, err := b.call(ctx, http.MethodGet, b.endpoint(attachmentPath(issueRef, name), nil),
		nil, "", &attachment)
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}

// attachmentPath addresses one attachment: the issue it belongs to and its
// name, which is the pair that identifies it. Both are escaped — a name may
// hold anything but a slash.
func attachmentPath(issueRef, name string) string {
	return "/api/issues/" + url.PathEscape(issueRef) + "/attachments/" + url.PathEscape(name)
}

func (b *Backend) ListAttachments(ctx context.Context, issueRef string,
	limit, offset *int) (backend.AttachmentPage, error) {
	query := url.Values{}
	if limit != nil {
		query.Set("limit", strconv.Itoa(*limit))
	}
	if offset != nil {
		query.Set("offset", strconv.Itoa(*offset))
	}

	attachments := []domain.Attachment{}
	header, err := b.call(ctx, http.MethodGet,
		b.endpoint("/api/issues/"+url.PathEscape(issueRef)+"/attachments", query),
		nil, "", &attachments)
	if err != nil {
		return backend.AttachmentPage{}, err
	}
	return backend.AttachmentPage{
		Attachments: attachments,
		Total:       totalCount(header, len(attachments)),
	}, nil
}

// OpenAttachment reads the metadata and then opens the content, which is two
// requests because the content endpoint answers with bytes and nothing else.
// The reader is the response body, and closing it is the caller's job.
func (b *Backend) OpenAttachment(ctx context.Context, issueRef, name string) (
	*domain.Attachment, io.ReadCloser, error) {
	attachment, err := b.GetAttachment(ctx, issueRef, name)
	if err != nil {
		return nil, nil, err
	}

	// Addressed by the resolved issue id rather than by the reference the caller
	// wrote, so the two requests cannot resolve an issue prefix differently and
	// answer for two different issues.
	endpoint := b.endpoint(attachmentPath(attachment.Issue, attachment.Name)+"/content", nil)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, awberr.Wrap(awberr.Runtime, err, "build request")
	}
	b.authenticate(request)

	resp, err := b.contentClient.Do(request)
	if err != nil {
		return nil, nil, awberr.Runtimef("cannot reach %s: %s", b.base.Host, err.Error())
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close() //nolint:errcheck // the response is being discarded
		return nil, nil, b.apiError(resp)
	}
	return attachment, resp.Body, nil
}

func (b *Backend) DeleteAttachment(ctx context.Context, issueRef, name string) (
	*domain.Attachment, error) {
	var attachment domain.Attachment
	_, err := b.call(ctx, http.MethodDelete, b.endpoint(attachmentPath(issueRef, name), nil),
		nil, "", &attachment)
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}
