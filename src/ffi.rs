//! Minimal C ABI surface for Go cgo wrappers.
//! This bypasses missing official UniFFI Go backend by exposing a thin C layer.
//! Functions return 0 on success, non-zero error codes. Caller frees allocated buffers.

use std::{os::raw::c_int, sync::Arc, ptr};

use crate::{
    codec::{error::Sv2CodecError, state::Sv2CodecState, decoder::Sv2Decoder, encoder::Sv2Encoder},
    messages::{Sv2Message, common::{SetupConnection, SetupConnectionSuccess}},
};

// Simple error codes (expand as needed)
const ERR_OK: c_int = 0;
const ERR_GENERIC: c_int = 1;
const ERR_CODEC: c_int = 2;
const ERR_MESSAGE: c_int = 3;
// Detailed codec error codes (non-zero unique values)
const ERR_LOCK: c_int = 10;
const ERR_BAD_KEY: c_int = 11;
const ERR_CREATE_INITIATOR: c_int = 12;
const ERR_CREATE_RESPONDER: c_int = 13;
const ERR_HANDSHAKE_0: c_int = 14;
const ERR_HANDSHAKE_1: c_int = 15;
const ERR_HANDSHAKE_2: c_int = 16;
const ERR_BAD_INIT_FRAME: c_int = 17;
const ERR_BAD_RESP_FRAME: c_int = 18;
const ERR_ENCODE_FRAME: c_int = 19;
const ERR_DECODE_FRAME: c_int = 20;
const ERR_FRAME_HEADER: c_int = 21;
const ERR_MESSAGES: c_int = 22;
const ERR_MISSING_BYTES: c_int = 23;
const ERR_INVALID_DATA_SIZE: c_int = 24;

#[repr(C)]
pub struct Sv2Buffer {
    pub data: *mut u8,
    pub len: usize,
}

unsafe fn alloc_buffer(bytes: Vec<u8>) -> Sv2Buffer {
    let len = bytes.len();
    let mut boxed = bytes.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    std::mem::forget(boxed);
    Sv2Buffer { data: ptr, len }
}

#[no_mangle]
pub extern "C" fn sv2_free_buffer(buf: Sv2Buffer) {
    if buf.data.is_null() { return; }
    unsafe {
        let _ = Vec::from_raw_parts(buf.data, buf.len, buf.len);
    }
}

#[no_mangle]
pub extern "C" fn sv2_new_initiator(authority_ptr: *const u8, len: usize, out_state: *mut *mut Sv2CodecState) -> c_int {
    if authority_ptr.is_null() || len != 32 { return ERR_MESSAGE; }
    let slice = unsafe { std::slice::from_raw_parts(authority_ptr, len) };
    match Sv2CodecState::new_initiator(slice.to_vec()) {
        Ok(state) => { unsafe { *out_state = Box::into_raw(Box::new(state)); }; ERR_OK }
        Err(_) => ERR_CODEC,
    }
}

#[no_mangle]
pub extern "C" fn sv2_codec_state_free(state: *mut Sv2CodecState) {
    if state.is_null() { return; }
    unsafe { drop(Box::from_raw(state)); }
}

#[no_mangle]
pub extern "C" fn sv2_codec_error_is_missing_bytes(code: c_int) -> c_int {
    if code == ERR_MISSING_BYTES { 1 } else { 0 }
}

#[no_mangle]
pub extern "C" fn sv2_step_0(state: *mut Sv2CodecState, out_buf: *mut Sv2Buffer) -> c_int {
    if state.is_null() { return ERR_GENERIC; }
    let s = unsafe { &*state };
    match s.step_0() {
        Ok(bytes) => unsafe { *out_buf = alloc_buffer(bytes); ERR_OK },
        Err(_) => ERR_CODEC,
    }
}

#[no_mangle]
pub extern "C" fn sv2_step_2(state: *mut Sv2CodecState, responder_ptr: *const u8, len: usize) -> c_int {
    if state.is_null() || responder_ptr.is_null() { return ERR_GENERIC; }
    let s = unsafe { &*state };
    let frame = unsafe { std::slice::from_raw_parts(responder_ptr, len) }.to_vec();
    match s.step_2(frame) { Ok(()) => ERR_OK, Err(_) => ERR_CODEC }
}

#[no_mangle]
pub extern "C" fn sv2_handshake_complete(state: *mut Sv2CodecState, out_complete: *mut c_int) -> c_int {
    if state.is_null() { return ERR_GENERIC; }
    let s = unsafe { &*state };
    match s.handshake_complete() {
        Ok(done) => { unsafe { *out_complete = if done {1} else {0}; }; ERR_OK }
        Err(_) => ERR_CODEC,
    }
}

#[no_mangle]
pub extern "C" fn sv2_encoder_new(out_encoder: *mut *mut Sv2Encoder) -> c_int {
    let enc = Sv2Encoder::new();
    unsafe { *out_encoder = Box::into_raw(Box::new(enc)); }
    ERR_OK
}

#[no_mangle]
pub extern "C" fn sv2_encoder_free(enc: *mut Sv2Encoder) { if enc.is_null() { return; } unsafe { drop(Box::from_raw(enc)); } }

#[no_mangle]
pub extern "C" fn sv2_decoder_new(out_decoder: *mut *mut Sv2Decoder) -> c_int {
    let dec = Sv2Decoder::new();
    unsafe { *out_decoder = Box::into_raw(Box::new(dec)); }
    ERR_OK
}

#[no_mangle]
pub extern "C" fn sv2_decoder_free(dec: *mut Sv2Decoder) { if dec.is_null() { return; } unsafe { drop(Box::from_raw(dec)); } }

#[no_mangle]
pub extern "C" fn sv2_decoder_buffer_size(dec: *mut Sv2Decoder, out_size: *mut u32) -> c_int {
    if dec.is_null() { return ERR_GENERIC; }
    let d = unsafe { &*dec };
    match d.buffer_size() { Ok(sz) => unsafe { *out_size = sz; ERR_OK }, Err(_) => ERR_CODEC }
}

// SetupConnection creation (subset)
#[no_mangle]
pub extern "C" fn sv2_setup_connection_new(
    protocol: u8,
    min_version: u16,
    max_version: u16,
    flags: u32,
    endpoint_host: *const u8, endpoint_host_len: usize,
    vendor: *const u8, vendor_len: usize,
    hardware_version: *const u8, hardware_version_len: usize,
    firmware: *const u8, firmware_len: usize,
    device_id: *const u8, device_id_len: usize,
    out_message: *mut *mut Sv2Message,
) -> c_int {
    unsafe fn str_from(ptr: *const u8, len: usize) -> Result<String,()> { if ptr.is_null() { return Err(()); } let s = std::slice::from_raw_parts(ptr, len); Ok(String::from_utf8_lossy(s).to_string()) }
    let endpoint_host = unsafe { str_from(endpoint_host, endpoint_host_len) }.map_err(|_| ()) ;
    let vendor = unsafe { str_from(vendor, vendor_len) }.map_err(|_| ()) ;
    let hardware_version = unsafe { str_from(hardware_version, hardware_version_len) }.map_err(|_| ()) ;
    let firmware = unsafe { str_from(firmware, firmware_len) }.map_err(|_| ()) ;
    let device_id = unsafe { str_from(device_id, device_id_len) }.map_err(|_| ()) ;
    if endpoint_host.is_err() || vendor.is_err() || hardware_version.is_err() || firmware.is_err() || device_id.is_err() { return ERR_MESSAGE; }
    let setup = SetupConnection { protocol, min_version, max_version, flags, endpoint_host: endpoint_host.unwrap(), endpoint_port: 0, vendor: vendor.unwrap(), hardware_version: hardware_version.unwrap(), firmware: firmware.unwrap(), device_id: device_id.unwrap() };
    let msg = Sv2Message::SetupConnection(setup);
    unsafe { *out_message = Box::into_raw(Box::new(msg)); }
    ERR_OK
}

#[no_mangle]
pub extern "C" fn sv2_message_free(msg: *mut Sv2Message) { if msg.is_null() { return; } unsafe { drop(Box::from_raw(msg)); } }

#[no_mangle]
pub extern "C" fn sv2_encode(
    enc: *mut Sv2Encoder,
    msg: *mut Sv2Message,
    state: *mut Sv2CodecState,
    out_buf: *mut Sv2Buffer,
) -> c_int {
    if enc.is_null() || msg.is_null() || state.is_null() { return ERR_GENERIC; }
    let enc_ref = unsafe { &*enc };
    let msg_ref = unsafe { &*msg };
    // Manually duplicate only supported variants to avoid requiring Clone across all message types.
    let msg_copy = match msg_ref {
        Sv2Message::SetupConnection(sc) => Sv2Message::SetupConnection(SetupConnection {
            protocol: sc.protocol,
            min_version: sc.min_version,
            max_version: sc.max_version,
            flags: sc.flags,
            endpoint_host: sc.endpoint_host.clone(),
            endpoint_port: sc.endpoint_port,
            vendor: sc.vendor.clone(),
            hardware_version: sc.hardware_version.clone(),
            firmware: sc.firmware.clone(),
            device_id: sc.device_id.clone(),
        }),
        Sv2Message::SetupConnectionSuccess(s) => Sv2Message::SetupConnectionSuccess(crate::messages::common::SetupConnectionSuccess { used_version: s.used_version, flags: s.flags }),
        Sv2Message::SetupConnectionError(e) => Sv2Message::SetupConnectionError(crate::messages::common::SetupConnectionError { flags: e.flags, error_code: e.error_code.clone() }),
        _ => return ERR_MESSAGE,
    };
    let state_arc = unsafe { Arc::from_raw(state) }; // temporarily build Arc
    let res = enc_ref.encode(msg_copy, state_arc.clone());
    let _ = Arc::into_raw(state_arc); // Avoid dropping original state
    match res { Ok(bytes) => unsafe { *out_buf = alloc_buffer(bytes); ERR_OK }, Err(_) => ERR_CODEC }
}

#[no_mangle]
pub extern "C" fn sv2_try_decode(
    dec: *mut Sv2Decoder,
    state: *mut Sv2CodecState,
    data_ptr: *const u8,
    data_len: usize,
    out_msg: *mut *mut Sv2Message,
    out_missing: *mut u32,
) -> c_int {
    if dec.is_null() || state.is_null() { return ERR_GENERIC; }
    let dec_ref = unsafe { &*dec };
    let state_arc = unsafe { Arc::from_raw(state) }; // temp arc
    let data = if data_ptr.is_null() || data_len == 0 { Vec::new() } else { unsafe { std::slice::from_raw_parts(data_ptr, data_len).to_vec() } };
    let res = dec_ref.try_decode(data, state_arc.clone());
    let _ = Arc::into_raw(state_arc);
    match res {
        Ok(message) => { unsafe { *out_msg = Box::into_raw(Box::new(message)); *out_missing = 0; }; ERR_OK }
        Err(Sv2CodecError::MissingBytes) => { // ask for more bytes
            match dec_ref.buffer_size() { Ok(sz) => unsafe { *out_missing = sz; }, Err(_) => unsafe { *out_missing = 0; } }
            ERR_MISSING_BYTES
        }
        Err(Sv2CodecError::LockError) => ERR_LOCK,
        Err(Sv2CodecError::BadKey) => ERR_BAD_KEY,
        Err(Sv2CodecError::FailedToCreateInitiator) => ERR_CREATE_INITIATOR,
        Err(Sv2CodecError::FailedToCreateResponder) => ERR_CREATE_RESPONDER,
        Err(Sv2CodecError::FailedHandshakeStep0) => ERR_HANDSHAKE_0,
        Err(Sv2CodecError::FailedHandshakeStep1) => ERR_HANDSHAKE_1,
        Err(Sv2CodecError::FailedHandshakeStep2) => ERR_HANDSHAKE_2,
        Err(Sv2CodecError::BadInitiatorFrame) => ERR_BAD_INIT_FRAME,
        Err(Sv2CodecError::BadResponderFrame) => ERR_BAD_RESP_FRAME,
        Err(Sv2CodecError::FailedToConvertMessageToFrame) => ERR_ENCODE_FRAME,
        Err(Sv2CodecError::FailedToDecodeFrame) => ERR_DECODE_FRAME,
        Err(Sv2CodecError::FailedToGetFrameHeader) => ERR_FRAME_HEADER,
        Err(Sv2CodecError::Sv2MessagesError(_)) => ERR_MESSAGES,
        Err(Sv2CodecError::InvalidDataSize { .. }) => ERR_INVALID_DATA_SIZE,
    }
}

#[no_mangle]
pub extern "C" fn sv2_is_setup_connection_success(msg: *const Sv2Message) -> c_int {
    if msg.is_null() { return 0; }
    let m = unsafe { &*msg };
    match m { Sv2Message::SetupConnectionSuccess(_) => 1, _ => 0 }
}

#[repr(C)]
pub struct SetupConnectionSuccessFields { pub used_version: u16, pub flags: u32 }

#[no_mangle]
pub extern "C" fn sv2_get_setup_connection_success(msg: *const Sv2Message, out_fields: *mut SetupConnectionSuccessFields) -> c_int {
    if msg.is_null() || out_fields.is_null() { return ERR_GENERIC; }
    let m = unsafe { &*msg };
    match m {
        Sv2Message::SetupConnectionSuccess(s) => {
            unsafe { *out_fields = SetupConnectionSuccessFields { used_version: s.used_version, flags: s.flags }; }
            ERR_OK
        }
        _ => ERR_MESSAGE,
    }
}
