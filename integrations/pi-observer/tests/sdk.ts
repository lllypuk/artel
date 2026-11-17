import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createAgentSession, DefaultResourceLoader, SessionManager, SettingsManager, ModelRuntime, VERSION } from "@earendil-works/pi-coding-agent";

assert.equal(VERSION, "1.5.2");
const cwd = process.cwd();
const agentDir = process.env.PI_CODING_AGENT_DIR!;
const settingsManager = SettingsManager.inMemory({ defaultProvider: "artel-fixture", defaultModel: "local", compaction: { enabled: false }, retry: { enabled: false } });
const loader = new DefaultResourceLoader({ cwd, agentDir, settingsManager,
  noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
  additionalExtensionPaths: [fileURLToPath(new URL("./probe.ts", import.meta.url)), fileURLToPath(new URL("./fixture.ts", import.meta.url))],
});
await loader.reload();
const modelRuntime = await ModelRuntime.create({ authPath: agentDir + "/auth.json", modelsPath: agentDir + "/models.json", allowModelNetwork: false });
const { session } = await createAgentSession({ cwd, agentDir, settingsManager, modelRuntime,
  resourceLoader: loader, sessionManager: SessionManager.create(cwd), noTools: "builtin" });
try {
  await session.bindExtensions({});
  const model = modelRuntime.getModel("artel-fixture", "local");
  assert.ok(model);
  await session.setModel(model);
  await new Promise(resolve => setTimeout(resolve, 100));
  assert.equal(session.messages.length, 0);
  await session.prompt("Fictional SDK input");
  const trace = readFileSync(process.env.ARTEL_PROBE_TRACE!, "utf8").trim().split("\n").map(line => JSON.parse(line));
  assert.equal(trace[0].event, "session_start");
  assert.ok(trace.some(row => row.event === "agent_settled" && row.idle));
  assert.equal(session.model?.provider, "artel-fixture");
  assert.deepEqual(loader.getSkills().skills, []);
  assert.deepEqual(loader.getAgentsFiles().agentsFiles, []);
  console.log("public SDK probe passed");
} finally {
  session.dispose();
}
