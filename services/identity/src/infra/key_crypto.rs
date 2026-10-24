use chacha20poly1305::aead::{Aead, KeyInit};
use chacha20poly1305::{Key, XChaCha20Poly1305, XNonce};
use rand::RngCore;

use crate::error::{AppError, AppResult};

pub struct KeyEncryptor {
    cipher: XChaCha20Poly1305,
}

impl KeyEncryptor {
    pub fn new(kek: &str) -> AppResult<Self> {
        let mut key_bytes = [0u8; 32];
        let src = kek.as_bytes();
        let len = src.len().min(32);
        key_bytes[..len].copy_from_slice(&src[..len]);
        if len < 32 {
            let mut hasher = sha2::Sha256::new();
            use sha2::Digest;
            hasher.update(src);
            let dig = hasher.finalize();
            key_bytes.copy_from_slice(&dig);
        }
        let key = Key::from_slice(&key_bytes);
        Ok(Self {
            cipher: XChaCha20Poly1305::new(key),
        })
    }

    pub fn encrypt(&self, plaintext: &[u8]) -> AppResult<Vec<u8>> {
        let mut nonce_bytes = [0u8; 24];
        rand::thread_rng().fill_bytes(&mut nonce_bytes);
        let nonce = XNonce::from_slice(&nonce_bytes);
        let ciphertext = self
            .cipher
            .encrypt(nonce, plaintext)
            .map_err(|_| AppError::Internal)?;
        let mut out = Vec::with_capacity(24 + ciphertext.len());
        out.extend_from_slice(&nonce_bytes);
        out.extend_from_slice(&ciphertext);
        Ok(out)
    }

    pub fn decrypt(&self, data: &[u8]) -> AppResult<Vec<u8>> {
        if data.len() < 25 {
            return Err(AppError::Internal);
        }
        let (nonce_bytes, ciphertext) = data.split_at(24);
        let nonce = XNonce::from_slice(nonce_bytes);
        self.cipher
            .decrypt(nonce, ciphertext)
            .map_err(|_| AppError::Internal)
    }
}
