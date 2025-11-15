package sv2

/*
// Link Rust shared library
#cgo LDFLAGS: -L../../target/release -lsv2 -ldl -lpthread
#include <stdlib.h>

typedef struct { unsigned char* data; size_t len; } Sv2Buffer;

// Forward declarations of C ABI (from Rust ffi.rs)
int sv2_new_initiator(const unsigned char* key, size_t len, void** out_state);
void sv2_codec_state_free(void* state);
int sv2_step_0(void* state, Sv2Buffer* out_buf);
int sv2_step_1(void* state, const unsigned char* data, size_t len, Sv2Buffer* out_buf);
int sv2_step_2(void* state, const unsigned char* data, size_t len);
int sv2_handshake_complete(void* state, int* out_complete);
int sv2_new_responder(const unsigned char* pubk, size_t publen, const unsigned char* privk, size_t privlen, size_t cert_secs, void** out_state);
int sv2_encoder_new(void** out_encoder);
void sv2_encoder_free(void* enc);
int sv2_decoder_new(void** out_decoder);
void sv2_decoder_free(void* dec);
int sv2_decoder_buffer_size(void* dec, unsigned int* out_size);
int sv2_setup_connection_new(unsigned char protocol, unsigned short min_version, unsigned short max_version, unsigned int flags,
    const unsigned char* endpoint_host, size_t endpoint_host_len,
    const unsigned char* vendor, size_t vendor_len,
    const unsigned char* hardware_version, size_t hardware_version_len,
    const unsigned char* firmware, size_t firmware_len,
    const unsigned char* device_id, size_t device_id_len,
    void** out_message);
void sv2_message_free(void* msg);
int sv2_encode(void* enc, void* msg, void* state, Sv2Buffer* out_buf);
int sv2_try_decode(void* dec, void* state, const unsigned char* data, size_t len, void** out_msg, unsigned int* out_missing);
void sv2_free_buffer(Sv2Buffer buf);
int sv2_is_setup_connection_success(const void* msg);
typedef struct { unsigned short used_version; unsigned int flags; } SetupConnectionSuccessFields;
int sv2_get_setup_connection_success(const void* msg, SetupConnectionSuccessFields* out_fields);
int sv2_codec_error_is_missing_bytes(int code);
int sv2_setup_connection_success_new(unsigned short used_version, unsigned int flags, void** out_message);
int sv2_is_setup_connection(const void* msg);
typedef struct { unsigned char protocol; unsigned short min_version; unsigned short max_version; unsigned int flags; } SetupConnectionFields;
int sv2_get_setup_connection(const void* msg, SetupConnectionFields* out_fields);
*/
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

var (
	ErrCodec   = errors.New("sv2 codec error")
	ErrMessage = errors.New("sv2 message error")
)

type CodecState struct{ ptr unsafe.Pointer }
type Encoder struct{ ptr unsafe.Pointer }
type Decoder struct{ ptr unsafe.Pointer }
type Message struct{ ptr unsafe.Pointer }
type SetupConnectionSuccess struct {
	UsedVersion uint16
	Flags       uint32
}
type SetupConnection struct {
	Protocol   uint8
	MinVersion uint16
	MaxVersion uint16
	Flags      uint32
}

// Allocate initiator
func NewInitiator(authorityKey32 []byte) (*CodecState, error) {
	if len(authorityKey32) != 32 {
		return nil, ErrMessage
	}
	var out unsafe.Pointer
	rc := C.sv2_new_initiator((*C.uchar)(unsafe.Pointer(&authorityKey32[0])), C.size_t(len(authorityKey32)), (*unsafe.Pointer)(&out))
	if rc != 0 {
		return nil, ErrCodec
	}
	return &CodecState{ptr: out}, nil
}

func (s *CodecState) Free() {
	if s.ptr != nil {
		C.sv2_codec_state_free(s.ptr)
		s.ptr = nil
	}
}

func (s *CodecState) Step0() ([]byte, error) {
	var buf C.Sv2Buffer
	rc := C.sv2_step_0(s.ptr, (*C.Sv2Buffer)(unsafe.Pointer(&buf)))
	if rc != 0 {
		return nil, ErrCodec
	}
	defer C.sv2_free_buffer(buf)
	return C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len)), nil
}

func (s *CodecState) Step2(frame []byte) error {
	if len(frame) == 0 {
		return ErrCodec
	}
	rc := C.sv2_step_2(s.ptr, (*C.uchar)(unsafe.Pointer(&frame[0])), C.size_t(len(frame)))
	if rc != 0 {
		return ErrCodec
	}
	return nil
}

func (s *CodecState) HandshakeComplete() (bool, error) {
	var out C.int
	rc := C.sv2_handshake_complete(s.ptr, &out)
	if rc != 0 {
		return false, ErrCodec
	}
	return out == 1, nil
}

// Responder API
func NewResponder(authorityPub32, authorityPriv32 []byte, certValiditySecs uint64) (*CodecState, error) {
	if len(authorityPub32) != 32 || len(authorityPriv32) != 32 {
		return nil, ErrMessage
	}
	var out unsafe.Pointer
	rc := C.sv2_new_responder((*C.uchar)(unsafe.Pointer(&authorityPub32[0])), C.size_t(len(authorityPub32)),
		(*C.uchar)(unsafe.Pointer(&authorityPriv32[0])), C.size_t(len(authorityPriv32)),
		C.size_t(certValiditySecs), (*unsafe.Pointer)(&out))
	if rc != 0 {
		return nil, ErrCodec
	}
	return &CodecState{ptr: out}, nil
}

func (s *CodecState) Step1(initiatorFrame []byte) ([]byte, error) {
	var buf C.Sv2Buffer
	rc := C.sv2_step_1(s.ptr, (*C.uchar)(unsafe.Pointer(&initiatorFrame[0])), C.size_t(len(initiatorFrame)), (*C.Sv2Buffer)(unsafe.Pointer(&buf)))
	if rc != 0 {
		return nil, ErrCodec
	}
	defer C.sv2_free_buffer(buf)
	return C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len)), nil
}

func NewEncoder() (*Encoder, error) {
	var out unsafe.Pointer
	if C.sv2_encoder_new((*unsafe.Pointer)(&out)) != 0 {
		return nil, ErrCodec
	}
	return &Encoder{ptr: out}, nil
}
func (e *Encoder) Free() {
	if e.ptr != nil {
		C.sv2_encoder_free(e.ptr)
		e.ptr = nil
	}
}

func NewDecoder() (*Decoder, error) {
	var out unsafe.Pointer
	if C.sv2_decoder_new((*unsafe.Pointer)(&out)) != 0 {
		return nil, ErrCodec
	}
	return &Decoder{ptr: out}, nil
}
func (d *Decoder) Free() {
	if d.ptr != nil {
		C.sv2_decoder_free(d.ptr)
		d.ptr = nil
	}
}

func (d *Decoder) BufferSize() (uint32, error) {
	var sz C.uint
	if C.sv2_decoder_buffer_size(d.ptr, &sz) != 0 {
		return 0, ErrCodec
	}
	return uint32(sz), nil
}

// SetupConnection message creation
func NewSetupConnection(protocol uint8, minVersion, maxVersion uint16, flags uint32, endpointHost, vendor, hardwareVersion, firmware, deviceID string) (*Message, error) {
	cEH := []byte(endpointHost)
	cV := []byte(vendor)
	cHW := []byte(hardwareVersion)
	cFW := []byte(firmware)
	cD := []byte(deviceID)
	var out unsafe.Pointer
	rc := C.sv2_setup_connection_new(C.uchar(protocol), C.ushort(minVersion), C.ushort(maxVersion), C.uint(flags),
		(*C.uchar)(unsafe.Pointer(&cEH[0])), C.size_t(len(cEH)),
		(*C.uchar)(unsafe.Pointer(&cV[0])), C.size_t(len(cV)),
		(*C.uchar)(unsafe.Pointer(&cHW[0])), C.size_t(len(cHW)),
		(*C.uchar)(unsafe.Pointer(&cFW[0])), C.size_t(len(cFW)),
		(*C.uchar)(unsafe.Pointer(&cD[0])), C.size_t(len(cD)),
		(*unsafe.Pointer)(&out))
	if rc != 0 {
		return nil, ErrMessage
	}
	return &Message{ptr: out}, nil
}

func (m *Message) Free() {
	if m.ptr != nil {
		C.sv2_message_free(m.ptr)
		m.ptr = nil
	}
}

func (e *Encoder) Encode(msg *Message, state *CodecState) ([]byte, error) {
	var buf C.Sv2Buffer
	rc := C.sv2_encode(e.ptr, msg.ptr, state.ptr, (*C.Sv2Buffer)(unsafe.Pointer(&buf)))
	if rc != 0 {
		return nil, ErrCodec
	}
	defer C.sv2_free_buffer(buf)
	return C.GoBytes(unsafe.Pointer(buf.data), C.int(buf.len)), nil
}

// TryDecode returns (*Message, missing, error). If missing > 0, caller should read that many bytes and retry.
func (d *Decoder) TryDecode(data []byte, state *CodecState) (*Message, uint32, error) {
	var outMsg unsafe.Pointer
	var missing C.uint
	var dataPtr *C.uchar
	if len(data) > 0 {
		dataPtr = (*C.uchar)(unsafe.Pointer(&data[0]))
	}
	rc := C.sv2_try_decode(d.ptr, state.ptr, dataPtr, C.size_t(len(data)), (*unsafe.Pointer)(&outMsg), &missing)
	if rc == 0 {
		return &Message{ptr: outMsg}, 0, nil
	}
	if C.sv2_codec_error_is_missing_bytes(rc) == 1 {
		return nil, uint32(missing), ErrCodec
	}
	// For other codes, missing might be zero or leftover; treat as fatal
	return nil, 0, fmt.Errorf("codec error code %d", int(rc))
}

func (m *Message) IsSetupConnectionSuccess() bool {
	if m.ptr == nil {
		return false
	}
	rc := C.sv2_is_setup_connection_success(m.ptr)
	return rc == 1
}

func (m *Message) GetSetupConnectionSuccess() (*SetupConnectionSuccess, error) {
	if !m.IsSetupConnectionSuccess() {
		return nil, ErrMessage
	}
	var fields C.SetupConnectionSuccessFields
	rc := C.sv2_get_setup_connection_success(m.ptr, &fields)
	if rc != 0 {
		return nil, ErrCodec
	}
	return &SetupConnectionSuccess{UsedVersion: uint16(fields.used_version), Flags: uint32(fields.flags)}, nil
}

func (m *Message) IsSetupConnection() bool {
	if m.ptr == nil {
		return false
	}
	return C.sv2_is_setup_connection(m.ptr) == 1
}

func (m *Message) GetSetupConnection() (*SetupConnection, error) {
	var f C.SetupConnectionFields
	rc := C.sv2_get_setup_connection(m.ptr, &f)
	if rc != 0 {
		return nil, ErrMessage
	}
	return &SetupConnection{Protocol: uint8(f.protocol), MinVersion: uint16(f.min_version), MaxVersion: uint16(f.max_version), Flags: uint32(f.flags)}, nil
}

func NewSetupConnectionSuccess(usedVersion uint16, flags uint32) (*Message, error) {
	var out unsafe.Pointer
	rc := C.sv2_setup_connection_success_new(C.ushort(usedVersion), C.uint(flags), (*unsafe.Pointer)(&out))
	if rc != 0 {
		return nil, ErrMessage
	}
	return &Message{ptr: out}, nil
}
