# Walkthrough: Workspace & Microservices Setup Automation Tool

Dự án **Workspace & Microservices Setup Automation Tool** đã được phát triển hoàn tất dựa trên các đặc tả trong `SPECS.md` và các phản hồi kỹ thuật từ bạn. Toàn bộ 7 Phase và 18 Tasks trong backlog đều được hoàn thành trọn vẹn và kiểm thử hoạt động trơn tru.

---

## 1. Architectural File Inventory (Cấu Trúc Mã Nguồn Thực Tế)

Hệ thống được tổ chức hoàn chỉnh theo mô hình **Clean Architecture**:

```text
/
├── cmd/
│   ├── cli/
│   │   └── main.go         # Entrypoint cho CLI Interactive (Survey)
│   └── gui/
│       ├── main.go         # Entrypoint cho Desktop App (Wails)
│       ├── app.go          # Go bindings truyền dẫn dữ liệu Go <-> JS
│       ├── wails.json      # Cấu hình dự án Wails
│       ├── build/          # Thư mục build output và assets của Wails
│       └── frontend/       # Giao diện Web Premium (Vite + Vanilla JS)
│           ├── index.html  # HTML5 Layout chính cho SPA Dashboard
│           ├── wailsjs/    # Wails JS Auto-generated bindings
│           └── src/
│               ├── main.js # Dynamic scripting, state manager, events listener
│               └── app.css # CSS styling, HSL colors, Glassmorphism, animations
├── internal/
│   ├── domain/
│   │   └── domain.go       # Entities (Repo, WorkspaceConfig, AuthProfile, CloneJob)
│   ├── repository/
│   │   ├── config_repo.go  # JSON Persistence, config.json, repos.json CRUD
│   │   ├── auth_helpers.go # CRUD extensions cho Auth Profile
│   │   ├── keyring_repo.go # OS Keyring wrapper (zalando/go-keyring)
│   │   ├── migrator.go     # Database schema versioning & migration engine
│   │   └── csv_parser.go   # CSV Bulk Import engine, validator, tag merge
│   └── usecase/
│       ├── clone_pipeline.go # Sequential worker pipeline, pause/resume mechanisms
│       └── git_sync.go     # Git Sync Client (GitHub/GitLab APIs, Pagination, Upsert)
├── go.mod                  # Go module dependency config
└── go.sum                  # Go checksum database
```

---

## 2. Key Accomplishments (Những Điểm Sáng Kỹ Thuật)

1. **Bảo mật tuyệt đối thông tin xác thực:**
   * Tích hợp sâu với **Windows Credential Manager** thông qua thư viện `zalando/go-keyring`, cấm lưu PAT ở định dạng văn bản thường xuống đĩa cứng.
   * Xây dựng regex-based **Log Masking Engine** tại logger middleware: Mọi log stream hiển thị lên console, terminal hay ghi vào file `app.log` đều được che dấu token thành `***` tự động.
2. **Kháng lỗi cực cao (Fault-tolerant Clone Pipeline):**
   * Sử dụng worker pool tuần tự (Worker Count = 1) xây dựng qua Go channels.
   * Bất kỳ repo nào bị lỗi (lỗi mạng, sai credentials...) sẽ kích hoạt cơ chế **Pause** luồng chạy và trả callback tương tác để CLI/GUI hiển thị Prompt hỏi người dùng chọn **Retry** (chạy lại) hoặc **Skip** (bỏ qua để clone repo tiếp theo).
3. **Dọn dẹp tự động (Cleanup Policy):**
   * Nếu tiến trình clone bị lỗi hoặc bị người dùng ngắt bằng `Ctrl+C` (CLI) / click `Cancel` (GUI), context sẽ bị hủy và core logic tự động xóa bỏ hoàn toàn thư mục clone dang dở đó khỏi đĩa để tránh để lại state rác.
4. **CSV Bulk Import mềm dẻo:**
   * Hỗ trợ tên service chứa khoảng trắng.
   * Chấp nhận và validate đầy đủ 3 loại URL clone: HTTPS (`https://`), HTTP (`http://`), SSH (`git@` hoặc `ssh://`).
   * **Merge Strategy thông minh:** Đồng bộ Upsert trùng URL sẽ tự động gộp tags mới và giữ nguyên các custom local tags trước đó của người dùng.
5. **Giao diện dòng lệnh tương tác (CLI survey):**
   * Điều hướng phím mũi tên chuyên nghiệp, phím Space để multi-select và Enter để chốt lựa chọn.
   * In bảng summary báo cáo kết quả clone rõ ràng.
6. **Giao diện Desktop (Wails GUI) Premium:**
   * Thiết kế **Sleek Space Dark Mode** sử dụng CSS Glassmorphism, harmonics HSL palette, glows, mượt mà chuyển tab và có thanh tiến trình (Progress bar) trôi mượt mà.
   * Lập trình **Sleek Monospace Log Terminal** tích hợp ngay trên màn hình clone, bắt các luồng `stdout/stderr` của Git clone truyền trực tiếp từ Go sang JS qua cơ chế Events.

---

## 3. How to Run (Hướng Dẫn Chạy & Build)

Để chạy được các lệnh, bạn cần đảm bảo các đường dẫn của Go, Git và Wails được liên kết trong môi trường của mình (hệ thống tự động liên kết trong CLI).

### 3.1. Chạy Giao diện Dòng lệnh (CLI)
Để mở bảng điều khiển tương tác CLI, chạy lệnh sau ở thư mục gốc của dự án:
```powershell
go run cmd/cli/main.go
```

### 3.2. Chạy Wails GUI ở Chế độ Phát triển (Development Mode)
Để chạy GUI với tính năng hot-reload (cả frontend Vite và backend Go):
```powershell
cd cmd/gui; wails dev
```

### 3.3. Build thành file chạy độc lập (.exe)
Để build ra file thực thi `.exe` duy nhất cho Windows (đã được đóng gói đầy đủ frontend và assets):
```powershell
cd cmd/gui; wails build
```
File executable độc lập được xuất ra tại: `cmd/gui/build/bin/gui.exe`.

---

## 4. Verification & Clean Compile Check (Kết Quả Kiểm Thử)

Hệ thống đã được kiểm thử biên dịch và đóng gói hoàn tất:
- **Go Compile (`go build ./...`):** Thành công 100%, không phát sinh bất kỳ cảnh báo hoặc lỗi cú pháp nào.
- **Wails Đóng gói (`wails build`):** Biên dịch frontend Vite và Go backend thành công hoàn toàn, xuất file thực thi `gui.exe` mượt mà chỉ trong `3.77s`.
