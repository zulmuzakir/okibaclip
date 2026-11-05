package capture

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

// Source is what the monitor needs of the system clipboard.
type Source interface {
	// Token is a cheap change counter, where the OS has one.
	Token() (uint64, bool)
	Formats() []transfer.Format
	NativeFormats() []string
	Read(formats ...transfer.Format) (transfer.Data, error)
	ReadFormat(f transfer.Format) ([]byte, error)
	// Owners are the apps that may have made the current copy, most likely
	// first; empty when unknown. The ignore list matches any of them, and
	// the first is recorded as the source.
	Owners() []Owner
}

// SystemClipboard is the Source backed by the real clipboard.
type SystemClipboard struct{}

func (SystemClipboard) Token() (uint64, bool) {
	noteFront()
	return changeToken()
}
func (SystemClipboard) Formats() []transfer.Format { return mygo.Clipboard.Formats() }
func (SystemClipboard) NativeFormats() []string    { return mygo.Clipboard.AvailableFormats() }
func (SystemClipboard) Owners() []Owner            { return clipOwners() }
func (SystemClipboard) ReadFormat(f transfer.Format) ([]byte, error) {
	return mygo.Clipboard.ReadFormat(f)
}
func (SystemClipboard) Read(formats ...transfer.Format) (transfer.Data, error) {
	return mygo.Clipboard.Read(formats...)
}
