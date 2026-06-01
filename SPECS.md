# ENGINEERING SPECS / IMPLEMENTATION CONTRACT
**Dự án:** Workspace & Microservices Setup Automation Tool
**Người soạn thảo:** Nguyễn Đình Giang
**Phiên bản:** 3.0 (Engineering Final Draft)
**Trạng thái:** Approved for Implementation

---

## 1. System Architecture (Kiến trúc Hệ thống)
Hệ thống tuân thủ nguyên tắc **Clean Architecture** để chia sẻ Core Logic giữa CLI và GUI (Wails). Cấu trúc mã nguồn (Go):
```text
/
├── cmd/
│   ├── cli/            # Entrypoint cho bản Go + Survey (Terminal)
│   └── gui/            # Entrypoint cho bản Wails (Desktop App)
├── internal/
│   ├── domain/         # Định nghĩa Entities, State Enums, Event Schemas
│   ├── usecase/        # Chứa Business Logic (Clone pipeline, Retry, Git Sync)
│   ├── repository/     # Logic đọc/ghi file JSON, tương tác OS Keyring
│   └── infrastructure/ # Adapter thực thi (Git CMD wrapper, File System)

```

---

## 2. Data Model & Persistence Strategy

### 2.1. Vị trí & Chiến lược lưu trữ

* **Vị trí:** `%APPDATA%\workspace-tool\` (Win), `~/Library/Application Support/workspace-tool/` (Mac), `~/.config/workspace-tool/` (Linux).
* **File JSON (`config.json`, `repos.json`):** Lưu metadata, danh sách repo, config người dùng.
* **OS Keyring:** Lưu credential (PAT, Passwords) thông qua `zalando/go-keyring`. Cấm lưu plain-text ra disk.

### 2.2. Data Model Schema

```go
type WorkspaceConfig struct {
    DefaultRootPath string `json:"default_root_path"`
    WorkerCount     int    `json:"worker_count"`
}

type Repo struct {
    ID            string   `json:"id"`
    Name          string   `json:"name"`
    URL           string   `json:"url"`
    AuthProfileID string   `json:"auth_profile_id"`
    Tags          []string `json:"tags"`
}

```

---

## 3. State Machine & Event Contract

### 3.1. Vòng đời Trạng thái (Job State Machine)

Mỗi một thao tác clone repo được quản lý bởi một State Machine chuẩn ngặt nghèo để tránh race condition:

* **States:** `PENDING` -> `CLONING` -> `SUCCESS` | `FAILED` | `CANCELLED`.
* **Transitions Rules:**
* Chỉ được chuyển sang `CLONING` khi state hiện tại là `PENDING` hoặc `FAILED` (do Retry).
* State `SUCCESS` và `CANCELLED` là Terminal States (không thể thay đổi).



### 3.2. Event Contract Schema

Hệ thống sử dụng Event-Driven để giao tiếp giữa Core Logic (Backend) và Presentation Layer (CLI/Wails Frontend). Standard Schema:

```json
// Gửi qua Wails runtime.EventsEmit hoặc CLI event channel
{
  "event_type": "CLONE_PROGRESS", // Các type: JOB_STARTED, CLONE_PROGRESS, JOB_COMPLETED, JOB_FAILED
  "payload": {
    "repo_id": "uuid-1234",
    "repo_name": "auth-service",
    "state": "CLONING",
    "message": "Receiving objects: 45% (153/340)",
    "error_code": null // Chỉ có value khi state = FAILED (VD: "ERR_AUTH_REJECTED")
  },
  "timestamp": "2026-05-29T18:04:36Z"
}

```

---

## 4. Execution Flow, Retry & Cancellation Policy

### 4.1. Core Execution Pipeline

* Sử dụng Worker Pool pattern (`Goroutines` + `Channels`). Worker hiện tại set = 1 (Sequential).
* Thực thi Git: `exec.CommandContext(ctx, "git", "clone", url, target_path)`.

### 4.2. Tương tác Lỗi & Retry Policy

* Khi Job chuyển state `FAILED`, worker tạm dừng (pause pipeline).
* Hệ thống trigger event yêu cầu user input: `Retry Policy Prompt`.
* User chọn **"Retry"**: Reset state về `PENDING`, worker thực thi lại clone job hiện tại.
* User chọn **"Skip"**: Đánh dấu Job là `FAILED`, worker tiếp tục consume job tiếp theo trong queue.

### 4.3. Cancellation & Cleanup Flow (Graceful Shutdown)

* **Trigger:** Nhận tín hiệu ngắt `Ctrl+C` (SIGINT/SIGTERM) hoặc user nhấn nút "Cancel" trên GUI.
* **Action:** Gọi `cancel()` function của `context.Context` đang bọc lệnh `os/exec`. Lệnh git clone đang chạy sẽ bị kill ngay lập tức (SIGKILL).
* **Cleanup Policy (Bắt buộc):** Nếu quá trình clone bị ngắt giữa chừng, core logic *phải* xóa thư mục đích (`Target_Dir/Service_Name`) vừa tạo dang dở. Không để lại state rác khiến lần chạy sau báo `ERR_DIR_EXISTS`.

---

## 5. Cụ thể hóa Git Provider Sync

Tránh abstract hóa, tính năng đồng bộ API (Sync) được định nghĩa chặt chẽ như sau:

* **Endpoint:** * GitHub: `GET https://api.github.com/user/repos?affiliation=owner,collaborator`
* GitLab: `GET https://gitlab.com/api/v4/projects?membership=true`


* **Auth:** Yêu cầu PAT lưu trong Keyring, truyền qua Header `Authorization: Bearer <token>`.
* **Pagination Handle:** Bắt buộc phải xử lý Header `Link` (GitHub) hoặc `X-Next-Page` (GitLab) để loop và fetch đầy đủ nếu user có hơn 100 repos (Tránh limit per page).
* **Merge Conflict Strategy:** Khi sync về local `repos.json`, thao tác này là *Upsert* (Update if exists by URL, Insert if not). Không tự động xóa các custom tags do user tự gán ở local.

---

## 6. Logging Architecture

* **Git Execution Logs (Stream Log):** Push trực tiếp lên giao diện thông qua Event Schema, không lưu ra đĩa cứng để tránh phình file.
* **App System Logs:**
* Vị trí: `~/.config/workspace-tool/logs/app.log`.
* Sử dụng thư viện `lumberjack` để rotate log (VD: Max size 10MB, giữ tối đa 5 file backup).
* Levels: `DEBUG`, `INFO`, `WARN`, `ERROR`.


* **Log Masking Policy:** Cài đặt ở middleware trước khi log ra file hoặc push qua event. Dùng Regex xóa bỏ mọi credential trong URL:
* Pattern: `(https?://)([^:]+):([^@]+)@(.*)` -> Replace `$3` thành `***`.



---

## 7. Performance & Cross-platform Handling

* Memory: CLI < 50MB RAM; Wails UI < 200MB RAM. App chịu tải danh sách hiển thị > 1000 repos.
* **Path Handling:** Bắt buộc dùng package `path/filepath`.
* **Shell Exection:** Cấm dùng `bash -c` / `cmd.exe /c`.
* **Error Catalog:** Định nghĩa cứng các hằng số: `ERR_GIT_NOT_FOUND`, `ERR_DIR_EXISTS`, `ERR_AUTH_REJECTED`, `ERR_NETWORK_TIMEOUT`.

---

## 8. Dependency Policy

* **Semantic Versioning:** Tuân thủ chặt chẽ SemVer cho mọi thư viện bên thứ 3 trong `go.mod`.
* **Vulnerability Audit:** Không sử dụng các module bị cờ báo lỗi bảo mật. Bắt buộc chạy `govulncheck` trước khi build release.
* **External Libs Lock:** Thư viện CLI (`Survey` hoặc `Bubbletea`) và GUI (`Wails`) phải chốt phiên bản (pinned version) để tránh breaking changes trong tương lai. Hạn chế tối đa dùng lệnh `replace` trong `go.mod`.

---

## 9. Acceptance Criteria (Tiêu chí Nghiệm thu - DoD)

1. **Chức năng:** Tool tạo thành công folder Task và clone toàn vẹn N repositories được chọn vào đúng thư mục, giữ đúng tên repo gốc.
2. **Kháng lỗi:** Khi clone một repo bị lỗi (sai mật khẩu/mất mạng), luồng thực thi dừng lại, yêu cầu người dùng Retry hoặc Skip. Tool không crash.
3. **Bảo mật:** Log in ra Terminal/GUI và log lưu vào file `app.log` tuyệt đối không chứa mật khẩu/token dạng plain-text (Masking thành `***`).
4. **Graceful Exit:** Nhấn `Ctrl+C` giữa lúc đang clone, tiến trình Git bị kill lập tức, thư mục đang clone dở dang bị xóa sạch khỏi ổ cứng.
5. **Multi-OS:** Build thành công file `.exe` cho Windows, executable/`.app` cho Mac/Linux mà core logic hoạt động không có độ lệch hành vi.

--

## 10. Functional Requirements (Yêu cầu chức năng chi tiết)

Phần này định nghĩa các chức năng (Features) bắt buộc hệ thống phải đáp ứng, được chia thành các nhóm logic cốt lõi.

### FR1: Quản lý Thư mục Làm việc (Workspace Management)
* **FR1.1. Cấu hình Thư mục Gốc:** Cho phép người dùng thiết lập và lưu trữ một đường dẫn thư mục gốc mặc định (Ví dụ: `D:\Workspaces`).
* **FR1.2. Khởi tạo Task Workspace:** * Hệ thống yêu cầu nhập mã/tên Task (Ví dụ: `TASK-1234`).
    * Hệ thống tự động đề xuất đường dẫn target: `[Thư-mục-gốc]/[Tên-Task]`.
    * Cho phép người dùng ghi đè (override) đường dẫn này thành một đường dẫn tùy ý trước khi bắt đầu luồng clone.

### FR2: Quản lý Nguồn dữ liệu Repository (Repo Source Management)
Hệ thống phải cung cấp 3 phương thức để nạp và quản lý danh sách các repositories:
* **FR2.1. Thao tác Thủ công (Manual CRUD):** * Cho phép người dùng Thêm mới, Chỉnh sửa, và Xóa thông tin của từng repository đơn lẻ.
    * Các trường dữ liệu bắt buộc: `Tên Service`, `URL`. 
    * Các trường tùy chọn: `Auth Profile ID`, `Tags`.
* **FR2.2. Import Hàng loạt (CSV Bulk Import):** Đọc file `.csv` chứa danh sách repo và map tự động vào file json lưu local.
* **FR2.3. Đồng bộ API (Git Provider Sync):** Gọi API của GitHub/GitLab (sử dụng PAT của người dùng) để lấy danh sách toàn bộ repo hiện có và lưu vào hệ thống local. Hệ thống thực hiện cơ chế Upsert (Cập nhật nếu đã có, Thêm mới nếu chưa có).

### FR3: Quản lý Xác thực (Authentication Management)
* **FR3.1. Quản lý Auth Profiles:** Cho phép người dùng tạo, sửa, xóa các "Hồ sơ xác thực" (Auth Profiles) chứa credential (PAT, password). Mật khẩu phải được đẩy thẳng vào OS Credential Manager.
* **FR3.2. Mapping Xác thực:** Khi định nghĩa một repo (ở FR2), người dùng có quyền chọn một Auth Profile cụ thể cho repo đó.
* **FR3.3. Fallback Authentication:** Cung cấp tính năng đánh dấu một Auth Profile là "Mặc định". Bất kỳ repo nào không được gán Auth Profile cụ thể sẽ tự động sử dụng profile mặc định này khi clone.

### FR4: Tương tác Người dùng & Lựa chọn (User Interaction & Selection)
* **FR4.1. Giao diện Chọn Repo (CLI Interactive):** * Hiển thị danh sách các repo hiện có dưới dạng list tương tác.
    * Hỗ trợ phím mũi tên (`Up/Down`) để điều hướng.
    * Hỗ trợ phím `Space` để chọn nhiều (Multi-select).
    * Hỗ trợ phím `Enter` để chốt danh sách.
* **FR4.2. Giao diện Chọn Repo (GUI):** Hiển thị danh sách dạng bảng/list, có checkbox, hỗ trợ thanh tìm kiếm (Search) và lọc theo Tag (Filter by Tags).

### FR5: Luồng Thực thi Clone (Clone Execution Pipeline)
* **FR5.1. Định tuyến Thư mục Đích:** Các service khi được clone về phải nằm trong Thư mục Task (đã chốt ở FR1.2) và **bắt buộc giữ nguyên tên gốc** của service đó.
* **FR5.2. Thực thi Tuần tự (Sequential Processing):** Hệ thống duyệt qua danh sách các repo đã chọn và thực thi lệnh clone từng cái một (Không chạy song song ở phiên bản 1.0).
* **FR5.3. Tracking Tiến độ (Real-time Progress & Logging):** * Hiển thị thanh tiến trình tổng quan (VD: `Đang xử lý 2/5`).
    * Capture và hiển thị trực tiếp luồng log đầu ra (`stdout/stderr`) của lệnh git đang chạy để người dùng theo dõi tốc độ mạng/số object được tải về.

### FR6: Xử lý Ngoại lệ & Quản lý Trạng thái (Exception & State Handling)
* **FR6.1. Xử lý Lỗi & Retry Prompt:** Nếu một lệnh clone thất bại (mất mạng, sai mật khẩu, thư mục đã tồn tại), hệ thống phải dừng luồng clone hiện tại, hiển thị cảnh báo lỗi màu đỏ nổi bật và cung cấp 2 tùy chọn cho người dùng:
    * *Retry:* Thử chạy lại lệnh clone cho service bị lỗi.
    * *Skip:* Bỏ qua service này, đánh dấu là FAILED và tiếp tục clone service tiếp theo.
* **FR6.2. Hủy bỏ an toàn (Graceful Cancellation):** Cho phép người dùng dừng toàn bộ tiến trình bằng `Ctrl+C` (CLI) hoặc nút Cancel (GUI).
* **FR6.3. Tự động Dọn rác (Cleanup Policy):** Khi luồng clone bị ngắt giữa chừng (do lỗi hệ thống hoặc user cố tình Hủy bỏ ở FR6.2), hệ thống bắt buộc phải tự động xóa bỏ thư mục chứa repo đang được clone dở dang để tránh lưu lại state rác.
* **FR6.4. Báo cáo Tổng kết (Summary Report):** Khi kết thúc (thành công toàn bộ hoặc có lỗi/skip), in ra màn hình bảng tổng kết liệt kê rõ những service nào đã clone thành công, service nào thất bại.
