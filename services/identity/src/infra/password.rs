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
    /// Tạo chính sách Argon2id với bộ nhớ tính bằng KiB, số vòng lặp và mức song song.
    /// Trả `Config` nếu bộ nhớ dưới 19.456 KiB, số vòng dưới 2, mức song song dưới 1
    /// hoặc tổ hợp tham số không hợp lệ với Argon2.
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

    /// Tạo bộ băm Argon2id phiên bản 0x13 theo chính sách; lỗi tham số thành `Internal`.
    fn hasher(&self) -> AppResult<Argon2<'static>> {
        let params = Params::new(self.memory_kib, self.iterations, self.parallelism, None)
            .map_err(|_| AppError::Internal)?;
        Ok(Argon2::new(Algorithm::Argon2id, Version::V0x13, params))
    }

    /// Băm mật khẩu bằng Argon2id với salt ngẫu nhiên, trả chuỗi PHC để lưu trữ.
    /// Chỉ nhận mật khẩu dài 8–128 byte; ngoài khoảng này trả `BadRequest`.
    /// Lỗi tạo bộ băm hoặc băm mật khẩu được trả dưới dạng `Internal`.
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

    /// Kiểm tra mật khẩu theo chuỗi PHC đã lưu.
    /// Mật khẩu sai, PHC không hợp lệ và mọi lỗi xử lý đều trở thành `Ok(false)`.
    pub fn verify(&self, password: &str, password_hash: &str) -> AppResult<bool> {
        let parsed = match PasswordHash::new(password_hash) {
            Ok(p) => p,
            Err(_) => return Ok(false),
        };
        let hasher = match self.hasher() {
            Ok(h) => h,
            Err(_) => return Ok(false),
        };
        match hasher.verify_password(password.as_bytes(), &parsed) {
            Ok(()) => Ok(true),
            Err(password_hash::Error::Password) => Ok(false),
            Err(_) => Ok(false),
        }
    }

    /// Cho biết hash cần băm lại nếu PHC không đọc được, thuật toán khác Argon2id
    /// hoặc một trong các tham số bộ nhớ, vòng lặp, mức song song khác chính sách.
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

impl PasswordPolicy {
    /// Thực hiện kiểm tra Argon2 giả để bổ sung chi phí xử lý cho lần đăng nhập bị từ chối.
    /// Bỏ qua kết quả và lỗi; không xác nhận một mật khẩu thực tế.
    pub fn dummy_verify(&self) {
        let hash = format!(
            "$argon2id$v=19$m={},t={},p=1$c29tZXNhbHRzb21lc2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            self.memory_kib,
            self.iterations
        );
        let Ok(parsed) = PasswordHash::new(&hash) else {
            return;
        };
        let Ok(hasher) = self.hasher() else {
            return;
        };
        let _ = hasher.verify_password(b"dummy-password-pad", &parsed);
    }
}
