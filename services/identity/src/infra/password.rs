use argon2::password_hash::{PasswordHash, PasswordHasher, PasswordVerifier, SaltString};
use argon2::{Algorithm, Argon2, Params, Version};
use password_hash::rand_core::OsRng;

use crate::error::{AppError, AppResult};

#[derive(Debug, Clone)]
pub struct PasswordPolicy {
    memory_kib: u32,
    iterations: u32,
    parallelism: u32,
}

impl PasswordPolicy {
    pub fn new(memory_kib: u32, iterations: u32, parallelism: u32) -> AppResult<Self> {
        if memory_kib < 19_456 {
            return Err(AppError::config("ARGON2_MEMORY_KIB must be >= 19456"));
        }
        if iterations < 2 {
            return Err(AppError::config("ARGON2_ITERATIONS must be >= 2"));
        }
        if parallelism < 1 {
            return Err(AppError::config("ARGON2_PARALLELISM must be >= 1"));
        }
        let _ = Params::new(memory_kib, iterations, parallelism, None)
            .map_err(|_| AppError::config("invalid Argon2 params"))?;
        Ok(Self {
            memory_kib,
            iterations,
            parallelism,
        })
    }

    fn hasher(&self) -> AppResult<Argon2<'static>> {
        let params = Params::new(self.memory_kib, self.iterations, self.parallelism, None)
            .map_err(|_| AppError::Internal)?;
        Ok(Argon2::new(Algorithm::Argon2id, Version::V0x13, params))
    }

    pub fn hash(&self, password: &str) -> AppResult<String> {
        if !(8..=128).contains(&password.len()) {
            return Err(AppError::BadRequest(
                "password length must be between 8 and 128".into(),
            ));
        }
        let salt = SaltString::generate(&mut OsRng);
        let hash = self
            .hasher()?
            .hash_password(password.as_bytes(), &salt)
            .map_err(|_| AppError::Internal)?
            .to_string();
        Ok(hash)
    }

    pub fn verify(&self, password: &str, password_hash: &str) -> AppResult<bool> {
        let parsed = match PasswordHash::new(password_hash) {
            Ok(p) => p,
            Err(_) => return Ok(false),
        };
        match Argon2::default().verify_password(password.as_bytes(), &parsed) {
            Ok(()) => Ok(true),
            Err(password_hash::Error::Password) => Ok(false),
            Err(_) => Ok(false),
        }
    }

    pub fn needs_rehash(&self, password_hash: &str) -> bool {
        let Ok(parsed) = PasswordHash::new(password_hash) else {
            return true;
        };
        if parsed.algorithm.as_str() != "argon2id" {
            return true;
        }
        let m = parsed
            .params
            .get("m")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        let t = parsed
            .params
            .get("t")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        let p = parsed
            .params
            .get("p")
            .and_then(|v| v.decimal().ok())
            .unwrap_or(0);
        m != self.memory_kib || t != self.iterations || p != self.parallelism
    }
}

pub fn dummy_verify() {
    let hash = "$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHRzb21lc2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
    let _ =
        Argon2::default().verify_password(b"dummy-password-pad", &PasswordHash::new(hash).unwrap());
}
