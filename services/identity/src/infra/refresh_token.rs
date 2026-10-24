use rand::RngCore;
use sha2::{Digest, Sha256};

/// Sinh refresh token từ 32 byte ngẫu nhiên, biểu diễn bằng 64 ký tự hex.
pub fn generate_opaque_token() -> String {
    let mut bytes = [0u8; 32];
    rand::thread_rng().fill_bytes(&mut bytes);
    hex::encode(bytes)
}

/// Trả SHA-256 dạng hex của chuỗi token để lưu thay cho token gốc.
pub fn hash_token(raw: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(raw.as_bytes());
    hex::encode(hasher.finalize())
}
