package imagefit

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// JPEG markers this file cares about. Every marker is 0xFF followed by one of
// these bytes.
const (
	markerSOI   = 0xD8 // start of image
	markerEOI   = 0xD9 // end of image
	markerSOS   = 0xDA // start of scan, followed by entropy-coded data
	markerTEM   = 0x01 // standalone, no length
	markerRST0  = 0xD0 // restart markers RST0..RST7 are standalone
	markerRST7  = 0xD7
	markerAPP1  = 0xE1 // Exif and XMP
	markerAPP13 = 0xED // IPTC and Photoshop
	markerCOM   = 0xFE // free-text comment
)

var errBadJPEG = errors.New("malformed JPEG structure")

// parseJPEG walks the marker segments of a JPEG once. It returns the EXIF
// orientation (1 when there is none, or it is unreadable) and a copy of the
// file with the APP1 (Exif and XMP), APP13 (IPTC and Photoshop) and COM
// segments left out. Those are where phones put GPS position, the camera
// serial number and free text, and notes sync between devices, so none of it
// should reach the vault. Every other segment is kept, notably APP0 (JFIF),
// APP2 (ICC colour profile) and APP14 (Adobe), because without them colours
// can shift. The compressed image data is copied byte for byte.
//
// Anything after the first EOI marker is dropped as well. It is not part of
// the image, and camera files use that space for embedded previews and video
// that carry their own metadata.
//
// When the structure cannot be followed to the end it returns an error, and
// the caller must not store the input: it falls back to a full re-encode,
// which writes no metadata at all.
func parseJPEG(data []byte) (orientation int, stripped []byte, err error) {
	orientation = 1
	if len(data) < 4 || data[0] != 0xFF || data[1] != markerSOI {
		return orientation, nil, errBadJPEG
	}
	out := make([]byte, 0, len(data))
	out = append(out, 0xFF, markerSOI)
	haveExif := false

	i := 2
	for i < len(data) {
		if data[i] != 0xFF {
			return orientation, nil, errBadJPEG
		}
		// Any number of 0xFF fill bytes may come before the marker byte.
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return orientation, nil, errBadJPEG
		}
		m := data[i]
		i++

		switch {
		case m == markerEOI:
			return orientation, append(out, 0xFF, markerEOI), nil
		case m == markerTEM || (m >= markerRST0 && m <= markerRST7):
			out = append(out, 0xFF, m)
			continue
		case m == 0x00 || m == markerSOI:
			return orientation, nil, errBadJPEG
		}

		// Everything else is a segment with a two-byte length that counts
		// itself.
		if i+2 > len(data) {
			return orientation, nil, errBadJPEG
		}
		n := int(binary.BigEndian.Uint16(data[i:]))
		if n < 2 || i+n > len(data) {
			return orientation, nil, errBadJPEG
		}
		seg := data[i : i+n]
		i += n

		if m == markerAPP1 && !haveExif && bytes.HasPrefix(seg[2:], []byte("Exif\x00\x00")) {
			haveExif = true
			orientation = exifOrientation(seg[2+6:])
		}
		if m == markerAPP1 || m == markerAPP13 || m == markerCOM {
			continue
		}
		out = append(out, 0xFF, m)
		out = append(out, seg...)

		if m != markerSOS {
			continue
		}
		// The entropy-coded scan runs until the next real marker. A 0xFF in
		// the data is escaped as FF 00, and restart markers sit inside it, so
		// neither ends the scan. Progressive files have several scans with
		// tables between them, which is why the walk goes on after this.
		j := i
		for {
			k := bytes.IndexByte(data[j:], 0xFF)
			if k < 0 || j+k+1 >= len(data) {
				return orientation, nil, errBadJPEG
			}
			j += k
			next := data[j+1]
			if next == 0x00 || (next >= markerRST0 && next <= markerRST7) {
				j += 2
				continue
			}
			if next == 0xFF {
				j++
				continue
			}
			break
		}
		out = append(out, data[i:j]...)
		i = j
	}
	// The data ran out before an EOI marker.
	return orientation, nil, errBadJPEG
}

// exifOrientation reads the orientation tag from the TIFF structure inside an
// Exif segment. It returns 1 (upright) for anything that is missing, out of
// range or truncated, because a wrong guess would rotate a good photo.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return 1
	}
	// The offsets come from the file, so compare them as uint64 to stay safe
	// on 32-bit builds.
	ifd := uint64(bo.Uint32(tiff[4:]))
	if ifd+2 > uint64(len(tiff)) {
		return 1
	}
	count := uint64(bo.Uint16(tiff[ifd:]))
	for e := uint64(0); e < count; e++ {
		at := ifd + 2 + e*12
		if at+12 > uint64(len(tiff)) {
			return 1
		}
		entry := tiff[at : at+12]
		if bo.Uint16(entry) != 0x0112 {
			continue
		}
		const short = 3
		if bo.Uint16(entry[2:]) != short || bo.Uint32(entry[4:]) != 1 {
			return 1
		}
		if v := int(bo.Uint16(entry[8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

var errBadPNG = errors.New("malformed PNG structure")

// stripPNG returns the PNG with its text and metadata chunks left out: eXIf
// (camera data, sometimes GPS), tEXt, zTXt and iTXt (free text that screenshot
// and editing tools fill with names, paths and settings) and tIME. Every other
// chunk is copied verbatim, so no checksum has to be recomputed and the pixels
// cannot change. Anything after IEND is dropped.
//
// The input is only stored when it beats Go's own re-encode, and that
// re-encode writes none of these chunks. So a file this cannot follow is
// simply not kept, and the caller falls back to the re-encode.
func stripPNG(data []byte) ([]byte, error) {
	const signature = "\x89PNG\r\n\x1a\n"
	if !bytes.HasPrefix(data, []byte(signature)) {
		return nil, errBadPNG
	}
	out := make([]byte, 0, len(data))
	out = append(out, signature...)
	i := len(signature)
	for {
		// Each chunk is a four-byte length, a four-byte type, the data and a
		// four-byte checksum. The length counts the data only.
		if i+12 > len(data) {
			return nil, errBadPNG
		}
		end := uint64(i) + 12 + uint64(binary.BigEndian.Uint32(data[i:]))
		if end > uint64(len(data)) {
			return nil, errBadPNG
		}
		typ := string(data[i+4 : i+8])
		switch typ {
		case "eXIf", "tEXt", "zTXt", "iTXt", "tIME":
		default:
			out = append(out, data[i:end]...)
		}
		if typ == "IEND" {
			return out, nil
		}
		i = int(end)
	}
}

var errBadWebP = errors.New("malformed WebP structure")

// stripWebP returns the WebP with its EXIF and XMP chunks left out, and the
// matching flag bits cleared in the VP8X header so a reader does not go
// looking for metadata that is gone. Every other chunk is copied verbatim,
// padding included, and the RIFF size is rewritten to match. Anything after
// the RIFF payload is dropped.
//
// A plain lossy or lossless WebP has no VP8X header and no metadata, and comes
// back the same. A file this cannot follow is re-encoded by the caller, which
// writes no metadata.
func stripWebP(data []byte) ([]byte, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, errBadWebP
	}
	riffEnd := 8 + uint64(binary.LittleEndian.Uint32(data[4:]))
	if riffEnd < 12 || riffEnd > uint64(len(data)) {
		return nil, errBadWebP
	}
	out := make([]byte, 12, riffEnd)
	copy(out, data[:12])

	const (
		flagsXMP  = 0x04
		flagsEXIF = 0x08
	)
	i := uint64(12)
	for i < riffEnd {
		if i+8 > riffEnd {
			return nil, errBadWebP
		}
		fourcc := string(data[i : i+4])
		size := uint64(binary.LittleEndian.Uint32(data[i+4:]))
		dataEnd := i + 8 + size
		if dataEnd > riffEnd {
			return nil, errBadWebP
		}
		// Chunks are padded to an even length. The pad of the last chunk is
		// sometimes left off by writers, and readers accept that too.
		end := dataEnd + size&1
		if end > riffEnd {
			end = dataEnd
		}

		switch fourcc {
		case "EXIF", "XMP ":
		case "VP8X":
			if size != 10 {
				return nil, errBadWebP
			}
			start := len(out)
			out = append(out, data[i:end]...)
			out[start+8] &^= flagsEXIF | flagsXMP
		default:
			out = append(out, data[i:end]...)
		}
		i = end
	}
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out, nil
}
