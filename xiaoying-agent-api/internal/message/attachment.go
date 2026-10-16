package message

import "strings"

const DirectoryAttachmentMimeType = "inode/directory"

func AttachmentIsDirectory(attachment Attachment) bool {
	mimeType := strings.ToLower(strings.TrimSpace(attachment.MimeType))
	return mimeType == DirectoryAttachmentMimeType || mimeType == "application/x-directory"
}

type Attachment struct {
	FilePath string
	FileName string
	MimeType string
	Content  []byte
}
