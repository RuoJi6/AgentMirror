import { spawnSync } from "node:child_process";
const name =
  process.platform === "win32" ? "bin/agentmirror.exe" : "bin/agentmirror";
const result = spawnSync(
  "go",
  ["build", "-trimpath", "-o", name, "./cmd/agentmirror"],
  {
    stdio: "inherit",
    env: { ...process.env, CGO_ENABLED: "0" },
  },
);
if (result.error) throw result.error;
process.exit(result.status ?? 1);
