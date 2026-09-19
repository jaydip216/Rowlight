#!/usr/bin/env node

const webdriver = (process.env.WEBDRIVER_URL ?? "http://127.0.0.1:4444").replace(/\/$/, "");
const rowlight = process.env.ROWLIGHT_URL;
const browserName = process.env.BROWSER_NAME ?? "safari";

if (!rowlight) {
  console.error("Set ROWLIGHT_URL to the authenticated URL printed by Rowlight.");
  process.exit(2);
}

async function command(path, body = {}) {
  const response = await fetch(`${webdriver}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const payload = await response.json();
  if (!response.ok || payload.value?.error) {
    throw new Error(payload.value?.message ?? `WebDriver request failed with ${response.status}`);
  }
  return payload.value;
}

const session = await command("/session", {
  capabilities: { alwaysMatch: { browserName } },
});
const sessionID = session.sessionId;

try {
  await command(`/session/${sessionID}/url`, { url: rowlight });
  const initial = await command(`/session/${sessionID}/execute/sync`, {
    script: `return {
      title: document.title,
      hash: location.hash,
      heading: document.querySelector('#connection-title')?.textContent,
      host: document.querySelector('input[name=host]')?.value,
      tokenStored: Boolean(sessionStorage.getItem('rowlight-launch-token'))
    }`,
    args: [],
  });
  if (initial.title !== "Rowlight" || initial.heading !== "Connect to MySQL / MariaDB" || initial.host !== "127.0.0.1") {
    throw new Error(`unexpected initial UI state: ${JSON.stringify(initial)}`);
  }
  if (initial.hash !== "" || !initial.tokenStored) {
    throw new Error(`launch token bootstrap failed: ${JSON.stringify(initial)}`);
  }

  await command(`/session/${sessionID}/refresh`);
  const afterRefresh = await command(`/session/${sessionID}/execute/async`, {
    script: `const done = arguments[arguments.length - 1];
      const token = sessionStorage.getItem('rowlight-launch-token');
      fetch('/api/health', { headers: { Authorization: 'Bearer ' + token } })
        .then(async response => done({ status: response.status, body: await response.json(), hash: location.hash }))
        .catch(error => done({ error: String(error) }));`,
    args: [],
  });
  if (afterRefresh.status !== 200 || afterRefresh.body?.status !== "ok" || afterRefresh.hash !== "") {
    throw new Error(`authenticated refresh failed: ${JSON.stringify(afterRefresh)}`);
  }
  console.log(`${browserName} smoke test passed`);
} finally {
  try {
    await fetch(`${webdriver}/session/${sessionID}`, { method: "DELETE" });
  } catch {
    // The driver may already have closed the session after a browser failure.
  }
}
