package derive

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/ethereum-optimism/optimism/op-service/eth"
)

type fakeChannelBankInput struct {
	origin eth.L1BlockRef
	data   []struct {
		frame Frame
		err   error
	}
}

func (f *fakeChannelBankInput) Origin() eth.L1BlockRef {
	return f.origin
}

func (f *fakeChannelBankInput) NextFrame(_ context.Context) (Frame, error) {
	if len(f.data) == 0 {
		return Frame{}, io.EOF
	}
	out := f.data[0]
	f.data = f.data[1:]
	return out.frame, out.err
}

func (f *fakeChannelBankInput) AddFrame(frame Frame, err error) {
	f.data = append(f.data, struct {
		frame Frame
		err   error
	}{frame: frame, err: err})
}

// ExpectNextFrameData takes a set of test frame & turns into the raw data
// for reading into the channel bank via `NextData`
func (f *fakeChannelBankInput) AddFrames(frames ...testFrame) {
	for _, frame := range frames {
		f.AddFrame(frame.ToFrame(), nil)
	}
}

var _ NextFrameProvider = (*fakeChannelBankInput)(nil)

// format: <channelID-data>:<frame-number>:<content><optional-last-frame-marker "!">
// example: "abc:0:helloworld!"
type testFrame string

func (tf testFrame) ChannelID() ChannelID {
	parts := strings.Split(string(tf), ":")
	return strChannelID(parts[0])
}

func strChannelID(s string) ChannelID {
	var chID ChannelID
	copy(chID[:], s)
	return chID
}

func (tf testFrame) FrameNumber() uint64 {
	parts := strings.Split(string(tf), ":")
	frameNum, err := strconv.ParseUint(parts[1], 0, 64)
	if err != nil {
		panic(err)
	}
	return frameNum
}

func (tf testFrame) IsLast() bool {
	parts := strings.Split(string(tf), ":")
	return strings.HasSuffix(parts[2], "!")
}

func (tf testFrame) Content() []byte {
	parts := strings.Split(string(tf), ":")
	return []byte(strings.TrimSuffix(parts[2], "!"))
}

func (tf testFrame) ToFrame() Frame {
	return Frame{
		ID:          tf.ChannelID(),
		FrameNumber: uint16(tf.FrameNumber()),
		Data:        tf.Content(),
		IsLast:      tf.IsLast(),
	}
}
