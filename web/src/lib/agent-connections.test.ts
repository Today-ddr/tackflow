import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { buildAgentGuide } from "./agent-connections";

const token = { id: 1, name: "phone", prefix: "tk_preview", scopes: ["capture"], project_ids: [4], tag_ids: [7], created_at: "2026-11-16T09:00:00Z", last_used_at: null, expires_at: null };

describe("copyable agent guide", () => {
  it("provides a runnable check command with exactly one destination and an environment credential", () => {
    const guide = buildAgentGuide("https://tack.example/api/v1", token);
    const command = guide.split("\n\n").find((part) => part.startsWith("curl "));
    expect(command).toBeDefined();
    const output = execFileSync("/bin/sh", ["-c", `curl() { printf '%s\\n' "$@"; }; TACK_API_TOKEN=fixture_key; ${command}`], { encoding: "utf8" });
    expect(output.trim().split("\n")).toEqual(["--fail-with-body", "--silent", "--show-error", "https://tack.example/api/v1/agent/connection", "-H", "Authorization: Bearer fixture_key"]);
  });

  it("includes permitted classification names for capture-only agents without suggesting read or claim", () => {
    const guide = buildAgentGuide("http://localhost:8080/api/v1", token, { projects: [{ id: 4, name: "Codeg" }], tags: [{ id: 7, name: "bug" }] });
    expect(guide).toContain('"name":"Codeg"');
    expect(guide).toContain('"name":"bug"');
    expect(guide).not.toContain("GET /tasks");
    expect(guide).not.toContain("POST /agent/claim");
    expect(guide).toContain("POST /capture");
    expect(guide).toContain("停止自动恢复，保留原 key/body，请用户在网页核实");
    expect(guide).not.toContain("先读现状");
  });

  it("prepares writes within the key's project and tag intersections", () => {
    const guide = buildAgentGuide("https://tack.example/api/v1", { ...token, scopes: ["write"] });
    expect(guide).toContain('"project_id":4');
    expect(guide).toContain('"tag_ids":[7]');
    expect(guide).toContain('"agent_ready":false');
    expect(guide).toContain("POST /agent/claim");
    expect(guide).toContain("/heartbeat");
    expect(guide).toContain('"status":"review"');
    expect(guide).toContain("先读现状");
    expect(guide).not.toContain(token.prefix);
  });

  it("makes recovery conditional on actual permissions in the general guide", () => {
    const guide = buildAgentGuide("https://tack.example/api/v1", null);
    expect(guide).toContain("有 read/write 权限时先读现状");
    expect(guide).toContain("只有 capture 时停止自动恢复，保留原 key/body，请用户在网页核实");
  });
});
