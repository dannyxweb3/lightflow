# Project instructions

Read REQUIREMENT.md and TECHNICAL_DESIGN.md before changing architecture.

- This checkout develops the Windows client: Tauri/React UI, Rust IPC bridge,
  Go local daemon and core/platform adapters. The Linux Codex checkout owns
  the control plane and gateway agent. Do not implement a competing backend.
- UI never handles proxy credentials, core configuration or network mutations.
- Keep shared IPC contracts in contracts/. Cloud API contracts must be synced
  with the server project before real authorization is implemented.
- Development simulation must be explicit, visible, and never claim to protect
  traffic. Production connections fail closed until real adapters are implemented.
- Do not log credentials, destinations, DNS queries or browsing history.
- Validate frontend with npm run build and npm test in apps/desktop.
  Validate Go with go test ./... and go vet ./... at repository root.
  Validate Rust with cargo check in apps/desktop/src-tauri.
- Never overwrite existing system routes, DNS or firewall policy during a UI demo.
