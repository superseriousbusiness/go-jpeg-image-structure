package exifjpeg

import (
	"bytes"
	"bufio"
	"fmt"

	"encoding/binary"

	"github.com/dsoprea/go-logging"
)

const (
	MARKER_EOI   = 0xd9
	MARKER_SOS   = 0xda
	MARKER_SOD   = 0x93
	MARKER_DQT   = 0xdb
	MARKER_APP0  = 0xe0
	MARKER_APP1  = 0xe1
	MARKER_APP2  = 0xe2
	MARKER_APP3  = 0xe3
	MARKER_APP4  = 0xe4
	MARKER_APP5  = 0xe5
	MARKER_APP6  = 0xe6
	MARKER_APP7  = 0xe7
	MARKER_APP8  = 0xe8
	MARKER_APP10 = 0xea
	MARKER_APP12 = 0xec
	MARKER_APP13 = 0xed
	MARKER_APP14 = 0xee
	MARKER_APP15 = 0xef
	MARKER_COM   = 0xfe
	MARKER_CME   = 0x64
	MARKER_SIZ   = 0x51

	MARKER_DHT = 0xc4
	MARKER_JPG = 0xc8
	MARKER_DAC = 0xcc

	MARKER_SOF0 = 0xc0
	MARKER_SOF1 = 0xc1
	MARKER_SOF2 = 0xc2
	MARKER_SOF3 = 0xc3
	MARKER_SOF5 = 0xc5
	MARKER_SOF6 = 0xc6
	MARKER_SOF7 = 0xc7
	MARKER_SOF9 = 0xc9
	MARKER_SOF10 = 0xca
	MARKER_SOF11 = 0xcb
	MARKER_SOF13 = 0xcd
	MARKER_SOF14 = 0xce
	MARKER_SOF15 = 0xcf
)

var (
	jpegLogger        = log.NewLogger("exifjpeg.jpeg")
	jpegMagicStandard = []byte{0xff, 0xd8, 0xff}
	jpegMagic2000     = []byte{0xff, 0x4f, 0xff}

	markerLen = map[byte]int{
		0x00: 0,
		0x01: 0,
		0xd0: 0,
		0xd1: 0,
		0xd2: 0,
		0xd3: 0,
		0xd4: 0,
		0xd5: 0,
		0xd6: 0,
		0xd7: 0,
		0xd8: 0,
		0xd9: 0,
		0xda: 0,

		// J2C
		0x30: 0,
		0x31: 0,
		0x32: 0,
		0x33: 0,
		0x34: 0,
		0x35: 0,
		0x36: 0,
		0x37: 0,
		0x38: 0,
		0x39: 0,
		0x3a: 0,
		0x3b: 0,
		0x3c: 0,
		0x3d: 0,
		0x3e: 0,
		0x3f: 0,
		0x4f: 0,
		0x92: 0,
		0x93: 0,

		// J2C extensions
		0x74: 4,
		0x75: 4,
		0x77: 4,
	}

	markerNames = map[byte]string {
		MARKER_EOI: "EOI",
		MARKER_SOS: "SOS",
		MARKER_SOD: "SOD",
		MARKER_DQT: "DQT",
		MARKER_APP0: "APP0",
		MARKER_APP1: "APP1",
		MARKER_APP2: "APP2",
		MARKER_APP3: "APP3",
		MARKER_APP4: "APP4",
		MARKER_APP5: "APP5",
		MARKER_APP6: "APP6",
		MARKER_APP7: "APP7",
		MARKER_APP8: "APP8",
		MARKER_APP10: "APP10",
		MARKER_APP12: "APP12",
		MARKER_APP13: "APP13",
		MARKER_APP14: "APP14",
		MARKER_APP15: "APP15",
		MARKER_COM: "COM",
		MARKER_CME: "CME",
		MARKER_SIZ: "SIZ",

		MARKER_DHT: "DHT",
		MARKER_JPG: "JPG",
		MARKER_DAC: "DAC",

		MARKER_SOF0: "SOF0",
		MARKER_SOF1: "SOF1",
		MARKER_SOF2: "SOF2",
		MARKER_SOF3: "SOF3",
		MARKER_SOF5: "SOF5",
		MARKER_SOF6: "SOF6",
		MARKER_SOF7: "SOF7",
		MARKER_SOF9: "SOF9",
		MARKER_SOF10: "SOF10",
		MARKER_SOF11: "SOF11",
		MARKER_SOF13: "SOF13",
		MARKER_SOF14: "SOF14",
		MARKER_SOF15: "SOF15",
	}
)

type SofSegment struct {
	BitsPerSample byte
	Width, Height uint16
	ComponentCount byte
}

func (ss SofSegment) String() string {
	return fmt.Sprintf("SOF<BitsPerSample=(%d) Width=(%d) Height=(%d) ComponentCount=(%d)>", ss.BitsPerSample, ss.Width, ss.Height, ss.ComponentCount)
}

type SegmentVisitor interface {
	HandleSegment(markerId byte, markerName string, counter int, lastIsScanData bool) error
}

type SofSegmentVisitor interface {
	HandleSof(sof *SofSegment) error
}

type JpegSplitter struct {
	lastMarkerId byte
	lastMarkerName string
	counter int
	lastIsScanData bool
	visitor interface{}
}

func NewJpegSplitter(visitor interface{}) *JpegSplitter {
	return &JpegSplitter{
		visitor: visitor,
	}
}

func (js *JpegSplitter) MarkerId() byte {
	return js.lastMarkerId
}

func (js *JpegSplitter) MarkerName() string {
	return js.lastMarkerName
}

func (js *JpegSplitter) Counter() int {
	return js.counter
}

func (js *JpegSplitter) IsScanData() bool {
	return js.lastIsScanData
}

func (js *JpegSplitter) processScanData(data []byte) (advanceBytes int, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	dataLength := len(data)

	found := false
	i := 0
	for ; i < dataLength - 1; i++ {
		if data[i] == 0xff && data[i + 1] == MARKER_EOI {
			found = true
			break
		}
	}

	if found == false {
		jpegLogger.Debugf(nil, "Not enough (2)")
		return 0, nil
	}

	// Jump past the current 0xff and marker bytes.
	// i += 2

	js.lastIsScanData = true
	js.lastMarkerId = 0
	js.lastMarkerName = ""

	// Note that we don't increment the counter since this isn't an actual
	// segment.

	jpegLogger.Debugf(nil, "End of scan-data.")

	err = js.handleSegment(00, nil)
	log.PanicIf(err)

	return i, nil
}

func (js *JpegSplitter) Split(data []byte, atEOF bool) (advance int, token []byte, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if js.counter == 0 {
		// Verify magic bytes.

		if len(data) < 3 {
			jpegLogger.Debugf(nil, "Not enough (1)")
			return 0, nil, nil
		}

		if data[0] == jpegMagic2000[0] && data[1] == jpegMagic2000[1] && data[2] == jpegMagic2000[2] {
			// TODO(dustin): Return to JPEG2000 support.
			log.Panicf("JPEG2000 not supported")
		}

		if data[0] != jpegMagicStandard[0] || data[1] != jpegMagicStandard[1] || data[2] != jpegMagicStandard[2] {
			log.Panicf("file does not look like a JPEG: (%X) (%X) (%X)", data[0], data[1], data[2])
		}
	}

// TODO(dustin): !! We're assuming that ignore atEOF and returning (0, nil, nil) when we need more data and there isn't any will raise an io.EOF (thereby delegating a redundant check to our caller). We might want to specifically run an example for this scenario.

	dataLength := len(data)

	jpegLogger.Debugf(nil, "SPLIT: LEN=(%d) COUNTER=(%d)", dataLength, js.counter)

	// If the last segment was the SOS, we're currently sitting on scan data.
	// Search for the EOI marker aferward in order to know how much data there
	// is. Return this as its own token.
	//
	// REF: https://stackoverflow.com/questions/26715684/parsing-jpeg-sos-marker
	if js.lastMarkerId == MARKER_SOS {
		advanceBytes, err := js.processScanData(data)
		log.PanicIf(err)

		// This will either return 0 and implicitly request that we need more
		// data and then need to run again or will return an actual byte count
		// to progress by.
		return advanceBytes, nil, nil
	} else {
		js.lastIsScanData = false
	}

	// If we're here, we're supposed to be sitting on the 0xff bytes at the
	// beginning of a segment (just before the marker).

	if data[0] != 0xff {
		log.Panicf("not on new segment marker: (%02X)", data[0])
	}

	i := 0
	found := false
	for ; i < dataLength; i++ {
		jpegLogger.Debugf(nil, "Prefix check: (%d) %02X", i, data[i])

		if data[i] != 0xff {
			found = true
			break
		}
	}

	jpegLogger.Debugf(nil, "Skipped by leading 0xFF bytes: (%d)", i)

	if found == false || i >= dataLength {
		jpegLogger.Debugf(nil, "Not enough (3)")
		return 0, nil, nil
	}

	markerId := data[i]
	jpegLogger.Debugf(nil, "MARKER-ID=%x", markerId)

	js.lastMarkerName = markerNames[markerId]

	sizeLen, found := markerLen[markerId]
	jpegLogger.Debugf(nil, "MARKER-ID=%x SIZELEN=%v FOUND=%v", markerId, sizeLen, found)

	i++

	b := bytes.NewBuffer(data[i:])
	payloadLength := 0

	if found == false {
		// It's not one of the static-length markers. Read the length.
		//
		// The length is an unsigned 16-bit network/big-endian.

		if i + 2 >= dataLength {
			jpegLogger.Debugf(nil, "Not enough (4)")
			return 0, nil, nil
		}

		len_ := uint16(0)
		err = binary.Read(b, binary.BigEndian, &len_)
		log.PanicIf(err)

		if len_ <= 2 {
			log.Panicf("length of size read for non-special marker (%02x) is unexpectedly not more than two.", markerId)
		}

		// (len_ includes the bytes of the length itself.)
		payloadLength = int(len_) - 2
		jpegLogger.Debugf(nil, "DataLength (dynamically-sized segment): (%d)", payloadLength)

		i += 2
	} else if sizeLen > 0 {
		// Accomodates the non-zero markers in our marker index, which only
		// represent J2C extensions.
		//
		// The length is an unsigned 32-bit network/big-endian.

		if sizeLen != 4 {
			log.Panicf("known non-zero marker is not four bytes, which is not currently handled: M=(%x)", markerId)
		}

		if i + 4 >= dataLength {
			jpegLogger.Debugf(nil, "Not enough (5)")
			return 0, nil, nil
		}

		len_ := uint32(0)
		err = binary.Read(b, binary.BigEndian, &len_)
		log.PanicIf(err)

		payloadLength = int(len_) - 4
		jpegLogger.Debugf(nil, "DataLength (four-byte-length segment): (%u)", len_)

		i += 4
	}

	jpegLogger.Debugf(nil, "PAYLOAD-LENGTH: %d", payloadLength)

	payload := data[i:]

	if payloadLength < 0 {
		log.Panicf("payload length less than zero: (%d)", payloadLength)
	}

	i += int(payloadLength)

	if i > dataLength {
		jpegLogger.Debugf(nil, "Not enough (6)")
		return 0, nil, nil
	}

	jpegLogger.Debugf(nil, "Found whole segment.")

	js.lastMarkerId = markerId

	payloadWindow := payload[:payloadLength]
	err = js.handleSegment(markerId, payloadWindow)
	log.PanicIf(err)

	js.counter++

	jpegLogger.Debugf(nil, "Returning advance of (%d)", i)

	return i, nil, nil
}

func (js *JpegSplitter) parseSof(data []byte) (sof *SofSegment, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	stream := bytes.NewBuffer(data)
	buffer := bufio.NewReader(stream)

	bitsPerSample, err := buffer.ReadByte()
	log.PanicIf(err)

	height := uint16(0)
	err = binary.Read(buffer, binary.BigEndian, &height)
	log.PanicIf(err)

	width := uint16(0)
	err = binary.Read(buffer, binary.BigEndian, &width)
	log.PanicIf(err)

	componentCount, err := buffer.ReadByte()
	log.PanicIf(err)

	sof = &SofSegment{
		BitsPerSample: bitsPerSample,
		Width: width,
		Height: height,
		ComponentCount: componentCount,
	}

	return sof, nil
}

func (js *JpegSplitter) isExif(data []byte) (ok bool) {
	if bytes.Compare(data[:6], []byte("Exif\000\000")) == 0 {
		return true
	}

	return false
}

func (js *JpegSplitter) parseAppData(markerId byte, data []byte) (err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if js.isExif(data) == true {
		byteOrderSignature := data[6:8]
		byteOrder := IfdByteOrder(BigEndianByteOrder)
		if string(byteOrderSignature) == "II" {
			byteOrder = IfdByteOrder(LittleEndianByteOrder)
		} else if string(byteOrderSignature) != "MM" {
			log.Panicf("byte-order not recognized: [%v]", byteOrderSignature)
		}

		ifd := NewIfd(data, byteOrder)

		visitor := func() error {
// TODO(dustin): !! Debugging.

			fmt.Printf("IFD visitor.\n")
			return nil
		}

		err := ifd.Scan(visitor)
		log.PanicIf(err)
	}

	return nil
}

func (js *JpegSplitter) handleSegment(markerId byte, payload []byte) (err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	sv, ok := js.visitor.(SegmentVisitor)
	if ok == true {
		err = sv.HandleSegment(js.lastMarkerId, js.lastMarkerName, js.counter, js.lastIsScanData)
		log.PanicIf(err)
	}

	if markerId >= MARKER_SOF0 && markerId <= MARKER_SOF15 {
		ssv, ok := js.visitor.(SofSegmentVisitor)
		if ok == true {
			sof, err := js.parseSof(payload)
			log.PanicIf(err)

			err = ssv.HandleSof(sof)
			log.PanicIf(err)
		}
	} else if markerId >= MARKER_APP0 && markerId <= MARKER_APP15 {
		err := js.parseAppData(markerId, payload)
		log.PanicIf(err)
	}

	return nil
}