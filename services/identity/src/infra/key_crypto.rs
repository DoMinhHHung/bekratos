use chacha20poly1305::aead::{Aead, KeyInit};
use chacha20poly1305::{Key, XChaCha20Poly1305, XNonce};
use rand::RngCore;
use sha2::{Digest, Sha256};

use crate::error::{AppError, AppResult};

pub struct KeyEncryptor {
    cipher: XChaCha20Poly1305,
    legacy_cipher: XChaCha20Poly1305,
}

fn derive_v1(kek: &str) -> [u8; 32] {
    let mut hasher = Sha256::new();
    hasher.update(b"bekratos-identity-kek-v1");
    hasher.update(kek.as_bytes());
    let dig = hasher.finalize();
    let mut key_bytes = [0u8; 32];
    key_bytes.copy_from_slice(&dig);
    key_bytes
}

fn derive_legacy(kek: &str) -> [u8; 32] {
    let mut key_bytes = [0u8; 32];
    let src = kek.as_bytes();
    let len = src.len().min(32);
    key_bytes[..len].copy_from_slice(&src[..len]);
    if len < 32 {
        let mut hasher = Sha256::new();
        hasher.update(src);
        let dig = hasher.finalize();
        key_bytes.copy_from_slice(&dig);
    }
    key_bytes
}

impl KeyEncryptor {
    pub fn new(kek: &str) -> AppResult<Self> {
        let v1 = derive_v1(kek);
        let legacy = derive_legacy(kek);
        Ok(Self {
            cipher: XChaCha20Poly1305::new(Key::from_slice(&v1)),
            legacy_cipher: XChaCha20Poly1305::new(Key::from_slice(&legacy)),
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
        self.decrypt_and_was_legacy(data).map(|(plain, _)| plain)
    }

    pub fn decrypt_and_was_legacy(&self, data: &[u8]) -> AppResult<(Vec<u8>, bool)> {
        if data.len() < 25 {
            return Err(AppError::Internal);
        }
        let (nonce_bytes, ciphertext) = data.split_at(24);
        let nonce = XNonce::from_slice(nonce_bytes);
        if let Ok(plain) = self.cipher.decrypt(nonce, ciphertext) {
            return Ok((plain, false));
        }
        let plain = self
            .legacy_cipher
            .decrypt(nonce, ciphertext)
            .map_err(|_| AppError::Internal)?;
        Ok((plain, true))
    }
}
