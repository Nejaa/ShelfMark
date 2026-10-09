package metadata

import (
	"archive/zip"
	"fmt"
)

// copyZIPMember preserves compressed file data but recreates directory headers.
// Some archives deflate an empty directory into a nonempty compressed stream.
// Writer.Copy passes those bytes to a directory writer, which rejects all data
// with "zip: write to directory". Directory entries have no content to copy.
func copyZIPMember(writer *zip.Writer, member *zip.File) error {
	var err error
	if member.FileInfo().IsDir() {
		header := member.FileHeader
		_, err = writer.CreateHeader(&header)
	} else {
		err = writer.Copy(member)
	}
	if err != nil {
		return fmt.Errorf("copy ZIP member %q: %w", member.Name, err)
	}
	return nil
}
