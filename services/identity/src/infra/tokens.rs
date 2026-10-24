use hmac::{Hmac, Mac};
use sha2::Sha256;
use subtle::ConstantTimeEq;
use uuid::Uuid;

use crate::error::{AppError, AppResult};

type HmacSha256 = Hmac<Sha256>;

#[derive(Debug, Clone)]
pub struct HmacKey {
    pub kid: String,
    pub secret: Vec<u8>,
}

#[derive(Debug, Clone)]
pub struct MagicLinkSigner {
    keys: Vec<HmacKey>,
    active_kid: String,
}

impl MagicLinkSigner {
    pub fn new(keys: Vec<HmacKey>, active_kid: String) -> AppResult<Self> {
        if keys.is_empty() {
            return Err(AppError::config("MAGIC_LINK_HMAC_KEYS must not be empty"));
        }
        if !keys.iter().any(|k| k.kid == active_kid) {
            return Err(AppError::config(
                "MAGIC_LINK_ACTIVE_KID must match a key in MAGIC_LINK_HMAC_KEYS",
            ));
        }
        for k in &keys {
            if k.secret.len() < 32 {
                return Err(AppError::config(format!(
                    "HMAC key {} must be at least 32 bytes",
                    k.kid
                )));
            }
        }
        Ok(Self { keys, active_kid })
    }

    fn active_key(&self) -> &HmacKey {
        self.keys
            .iter()
            .find(|k| k.kid == self.active_kid)
            .expect("active key validated at construction")
    }

    fn key_by_kid(&self, kid: &str) -> Option<&HmacKey> {
        self.keys.iter().find(|k| k.kid == kid)
    }

    fn canonical_message(purpose: &str, token_id: Uuid) -> Vec<u8> {
        let mut msg = Vec::with_capacity(64);
        msg.extend_from_slice(b"bekratos:v1:magic_link:");
        msg.extend_from_slice(purpose.as_bytes());
        msg.push(b':');
        msg.extend_from_slice(token_id.as_bytes());
        msg
    }

    fn mac_hex(secret: &[u8], message: &[u8]) -> AppResult<String> {
        let mut mac = HmacSha256::new_from_slice(secret).map_err(|_| AppError::Internal)?;
        mac.update(message);
        let result = mac.finalize().into_bytes();
        Ok(hex::encode(result))
    }

    pub fn issue(&self, purpose: &str, token_id: Uuid) -> AppResult<String> {
        let key = self.active_key();
        let msg = Self::canonical_message(purpose, token_id);
        let mac = Self::mac_hex(&key.secret, &msg)?;
        Ok(format!("{}.{}.{}", key.kid, token_id, mac))
    }

    pub fn verify(&self, purpose: &str, token: &str) -> AppResult<Uuid> {
        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AppError::BadRequest("invalid token format".into()));
        }
        let kid = parts[0];
        let token_id = Uuid::parse_str(parts[1])
            .map_err(|_| AppError::BadRequest("invalid token id".into()))?;
        let presented_mac = parts[2];
        if presented_mac.len() != 64 || !presented_mac.chars().all(|c| c.is_ascii_hexdigit()) {
            return Err(AppError::BadRequest("invalid token mac".into()));
        }

        let key = self
            .key_by_kid(kid)
            .ok_or_else(|| AppError::BadRequest("unknown token key".into()))?;

        let msg = Self::canonical_message(purpose, token_id);
        let expected = Self::mac_hex(&key.secret, &msg)?;

        let ok = expected.as_bytes().ct_eq(presented_mac.as_bytes()).into();
        if !ok {
            return Err(AppError::BadRequest("invalid token".into()));
        }
        Ok(token_id)
    }
}

pub fn parse_hmac_keys(raw: &str) -> AppResult<Vec<HmacKey>> {
    let mut keys = Vec::new();
    for part in raw.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        let (kid, hex_secret) = part
            .split_once(':')
            .ok_or_else(|| AppError::config("MAGIC_LINK_HMAC_KEYS entry must be kid:hex"))?;
        let secret = hex::decode(hex_secret.trim())
            .map_err(|_| AppError::config(format!("invalid hex for HMAC key {kid}")))?;
        keys.push(HmacKey {
            kid: kid.trim().to_string(),
            secret,
        });
    }
    if keys.is_empty() {
        return Err(AppError::config("MAGIC_LINK_HMAC_KEYS must not be empty"));
    }
    Ok(keys)
}
