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
	// Owner is the app the current contents came from; the zero Owner when
	// unknown.
	Owner() Owner
}

// SystemClipboard is the Source backed by the real clipboard.
type SystemClipboard struct{}

func (SystemClipboard) Token() (uint64, bool)      { return changeToken() }
func (SystemClipboard) Formats() []transfer.Format { return mygo.Clipboard.Formats() }
func (SystemClipboard) NativeFormats() []string    { return mygo.Clipboard.AvailableFormats() }
func (SystemClipboard) Owner() Owner               { return frontOwner() }
func (SystemClipboard) ReadFormat(f transfer.Format) ([]byte, error) {
	return mygo.Clipboard.ReadFormat(f)
}
func (SystemClipboard) Read(formats ...transfer.Format) (transfer.Data, error) {
	return mygo.Clipboard.Read(formats...)
}
