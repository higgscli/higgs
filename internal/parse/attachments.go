// Package parse extracts structured content (bodies, attachments) from
// RFC5322 messages.
package parse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/emersion/go-message"
)

// Attachment describes a single non-body MIME part inside an RFC822 message.
//
// Filename is parsed from Content-Disposition "filename" or falls back to
// Content-Type "name". It may be empty.
//
// ContentType is the media type (lowercased, no parameters).
//
// Disposition is one of "attachment", "inline", or "" when the header is
// absent.
//
// PartPath is the dotted multipart path (1-indexed, like IMAP) such as
// "1.2". It may be empty if derivation is not meaningful (top-level
// non-multipart message).
//
// Size is the byte length of the decoded payload.
//
// SHA256 is the lowercase hex digest of the decoded payload.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ContentID   string `json:"content_id"`
	Disposition string `json:"disposition"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	PartPath    string `json:"part_path"`
}

// decodedPart holds the decoded bytes of a single MIME leaf part along with
// sufficient metadata to build an Attachment. decoded may be nil if the
// part had a decode/charset error that we are choosing to swallow.
type decodedPart struct {
	mediaType   string
	disposition string
	filename    string
	contentID   string
	partPath    string
	data        []byte
}

// WalkAttachments parses rfc822 and returns metadata for every MIME part
// that should be surfaced as an attachment.
//
// A part is considered an attachment when either:
//   - Its Content-Disposition is "attachment", OR
//   - It is an inline non-text part (e.g. inline image in multipart/related)
//
// Text/plain and text/html bodies (inline or with no disposition) are
// excluded.
func WalkAttachments(rfc822 []byte) ([]Attachment, error) {
	parts, err := walkDecoded(rfc822)
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(parts))
	for _, p := range parts {
		if !shouldEmit(p) {
			continue
		}
		sum := sha256.Sum256(p.data)
		out = append(out, Attachment{
			Filename:    p.filename,
			ContentType: p.mediaType,
			ContentID:   p.contentID,
			Disposition: p.disposition,
			Size:        int64(len(p.data)),
			SHA256:      hex.EncodeToString(sum[:]),
			PartPath:    p.partPath,
		})
	}
	return out, nil
}

// ExtractAttachment decodes rfc822 and writes the first attachment matching
// the predicate to out. It returns the matched Attachment. If no attachment
// matches, os.ErrNotExist is returned.
func ExtractAttachment(rfc822 []byte, match func(Attachment) bool, out io.Writer) (Attachment, error) {
	parts, err := walkDecoded(rfc822)
	if err != nil {
		return Attachment{}, err
	}
	for _, p := range parts {
		if !shouldEmit(p) {
			continue
		}
		sum := sha256.Sum256(p.data)
		att := Attachment{
			Filename:    p.filename,
			ContentType: p.mediaType,
			ContentID:   p.contentID,
			Disposition: p.disposition,
			Size:        int64(len(p.data)),
			SHA256:      hex.EncodeToString(sum[:]),
			PartPath:    p.partPath,
		}
		if match != nil && !match(att) {
			continue
		}
		if _, err := out.Write(p.data); err != nil {
			return att, err
		}
		return att, nil
	}
	return Attachment{}, os.ErrNotExist
}

// ExtractAll writes every attachment passing filter (or every attachment if
// filter is nil) into dir. It returns metadata for each extracted file. The
// Attachment.Filename in the returned slice reflects the on-disk filename
// after sanitisation and collision resolution.
func ExtractAll(rfc822 []byte, dir string, filter func(Attachment) bool) ([]Attachment, error) {
	parts, err := walkDecoded(rfc822)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	used := map[string]struct{}{}
	out := []Attachment{}
	for _, p := range parts {
		if !shouldEmit(p) {
			continue
		}
		sum := sha256.Sum256(p.data)
		att := Attachment{
			Filename:    p.filename,
			ContentType: p.mediaType,
			ContentID:   p.contentID,
			Disposition: p.disposition,
			Size:        int64(len(p.data)),
			SHA256:      hex.EncodeToString(sum[:]),
			PartPath:    p.partPath,
		}
		if filter != nil && !filter(att) {
			continue
		}
		name := safeFilename(p.filename, p.mediaType)
		name = avoidCollision(name, used)
		used[name] = struct{}{}
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, p.data, 0o600); err != nil {
			return out, err
		}
		att.Filename = name
		out = append(out, att)
	}
	return out, nil
}

// walkDecoded parses rfc822 and returns decoded leaf parts with metadata.
// Multipart containers are descended; leaves are emitted in tree order with
// 1-indexed dotted part paths.
func walkDecoded(rfc822 []byte) ([]decodedPart, error) {
	if len(rfc822) == 0 {
		return nil, errors.New("empty rfc822")
	}
	ent, err := message.Read(bytes.NewReader(rfc822))
	if err != nil && ent == nil {
		return nil, err
	}
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, err
	}
	var parts []decodedPart
	walkErr := ent.Walk(func(path []int, entity *message.Entity, werr error) error {
		if entity == nil {
			return nil
		}
		// Skip multipart containers; they are not leaves.
		if mr := entity.MultipartReader(); mr != nil {
			return nil
		}
		mediaType, _, _ := entity.Header.ContentType()
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))

		disp, dispParams, _ := entity.Header.ContentDisposition()
		disp = strings.ToLower(strings.TrimSpace(disp))

		filename := ""
		if dispParams != nil {
			filename = dispParams["filename"]
		}
		if filename == "" {
			if _, ctParams, _ := entity.Header.ContentType(); ctParams != nil {
				filename = ctParams["name"]
			}
		}
		filename = strings.TrimSpace(filename)

		cid := strings.Trim(entity.Header.Get("Content-ID"), "<> \t")

		// Read the decoded body. Ignore read errors and treat as zero bytes so
		// hostile or malformed parts can still be emitted as metadata.
		var buf bytes.Buffer
		if entity.Body != nil {
			_, _ = io.Copy(&buf, entity.Body)
		}

		parts = append(parts, decodedPart{
			mediaType:   mediaType,
			disposition: disp,
			filename:    filename,
			contentID:   cid,
			partPath:    pathToString(path),
			data:        append([]byte(nil), buf.Bytes()...),
		})
		return nil
	})
	if walkErr != nil {
		return parts, walkErr
	}
	return parts, nil
}

// pathToString renders a 0-indexed walk path as a 1-indexed IMAP-style dotted
// string. An empty path (root, non-multipart) returns "".
func pathToString(path []int) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = strconv.Itoa(n + 1)
	}
	return strings.Join(parts, ".")
}

// shouldEmit returns true when the decoded part should be surfaced as an
// attachment. Plain/HTML body parts without an attachment disposition are
// skipped.
func shouldEmit(p decodedPart) bool {
	isText := strings.HasPrefix(p.mediaType, "text/plain") ||
		strings.HasPrefix(p.mediaType, "text/html")
	if p.disposition == "attachment" {
		return true
	}
	// No disposition, or "inline", or anything else: emit when non-text.
	return !isText
}

// safeFilename returns a filesystem-safe filename derived from raw and the
// media type. It strips path separators, NULs, and leading/trailing dots,
// caps length to 200 bytes, and falls back to a media-type derived name if
// raw is empty.
func safeFilename(raw, mediaType string) string {
	name := raw
	// Strip any directory components first.
	name = filepath.Base(name)
	// Replace traversal indicators and path separators defensively.
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "\x00", "_")
	// Trim leading/trailing dots and whitespace.
	name = strings.Trim(name, ". \t\r\n")
	// Fall back if the result is empty or contains nothing meaningful.
	if name == "" || name == "." || name == ".." || strings.Trim(name, "_") == "" {
		ext := ""
		if mediaType != "" {
			if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
				ext = exts[0]
			}
		}
		if ext == "" {
			ext = ".bin"
		}
		name = "attachment" + ext
	}
	if len(name) > 200 {
		// Preserve extension when trimming.
		ext := filepath.Ext(name)
		if len(ext) > 0 && len(ext) < 32 {
			base := name[:200-len(ext)]
			name = base + ext
		} else {
			name = name[:200]
		}
	}
	return name
}

// avoidCollision appends ".1", ".2" etc. to name until the result is not
// already present in used. The suffix is inserted before the extension.
func avoidCollision(name string, used map[string]struct{}) string {
	if _, clash := used[name]; !clash {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s.%d%s", base, i, ext)
		if _, clash := used[candidate]; !clash {
			return candidate
		}
	}
}
