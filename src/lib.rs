mod channels;
mod codec;
mod messages;
pub mod ffi; // C ABI for Go cgo wrappers

uniffi::setup_scaffolding!();
