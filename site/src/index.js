export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === '/robots.txt') {
      return new Response('User-agent: *\nAllow: /\n', { headers: { 'Content-Type': 'text/plain' } });
    }
    return new Response(HTML, {
      headers: {
        'Content-Type': 'text/html;charset=UTF-8',
        'Cache-Control': 'public, max-age=3600',
      },
    });
  },
};

const HTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>higgs — agent-first CLI for Proton Mail</title>
<meta name="description" content="A CLI built for AI agents to classify, label, and organize your Proton Mail. Local-only, NDJSON streaming, schema manifest, typed errors. No cloud, no telemetry.">
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>⚛️</text></svg>">
<style>
:root {
  --bg: #ffffff;
  --bg2: #f8f7fc;
  --surface: #f4f2fa;
  --surface2: #ede9f7;
  --border: #e5e0f0;
  --border2: #d4cbe8;
  --text: #1a1a2e;
  --text-dim: #6b7280;
  --text-faint: #9ca3af;
  --accent: #6d4aff;
  --accent-deep: #372580;
  --accent-hover: #5b3dff;
  --complement: #00b8d4;
  --complement-deep: #0094a8;
  --complement-soft: #e0f7ff;
  --warn: #f59e0b;
  --error: #ef4444;
  --success: #10b981;
  --mono: 'SF Mono', 'Fira Code', 'JetBrains Mono', 'Cascadia Code', monospace;
  --sans: -apple-system, BlinkMacSystemFont, 'Inter', 'Segoe UI', Helvetica, Arial, sans-serif;
  --shadow: 0 4px 24px rgba(109,74,255,0.08);
  --shadow-lg: 0 12px 48px rgba(109,74,255,0.12);
  --shadow-sm: 0 2px 8px rgba(26,26,46,0.06);
}
* { margin: 0; padding: 0; box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
  background: var(--bg);
  color: var(--text);
  font-family: var(--sans);
  line-height: 1.6;
  overflow-x: hidden;
  -webkit-font-smoothing: antialiased;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }

/* Nav */
nav {
  position: fixed; top: 0; width: 100%; z-index: 100;
  background: rgba(255,255,255,0.88);
  backdrop-filter: blur(20px) saturate(180%);
  -webkit-backdrop-filter: blur(20px) saturate(180%);
  border-bottom: 1px solid var(--border);
  padding: 0 24px;
  display: flex; align-items: center; justify-content: space-between;
  height: 60px;
}
nav .logo { font-weight: 700; font-size: 1.2rem; letter-spacing: -0.02em; color: var(--accent-deep); }
nav .logo span { color: var(--complement); }
nav ul { display: flex; gap: 28px; list-style: none; }
nav ul a { color: var(--text-dim); font-size: 0.9rem; font-weight: 500; transition: color .2s; }
nav ul a:hover { color: var(--accent-deep); text-decoration: none; }
nav .nav-cta {
  background: var(--accent); color: #fff; padding: 8px 18px;
  border-radius: 8px; font-size: 0.85rem; font-weight: 600;
  transition: transform .15s, box-shadow .15s;
  box-shadow: 0 2px 12px rgba(109,74,255,0.25);
}
nav .nav-cta:hover { text-decoration: none; transform: translateY(-1px); box-shadow: 0 4px 20px rgba(109,74,255,0.35); }

/* Hero */
.hero {
  min-height: 100vh; display: flex; flex-direction: column;
  align-items: center; justify-content: center;
  text-align: center; padding: 120px 24px 60px;
  position: relative;
  background: linear-gradient(180deg, var(--bg) 0%, var(--bg2) 100%);
}
.hero::before {
  content: ''; position: absolute; top: 0; left: 50%;
  transform: translateX(-50%);
  width: 900px; height: 500px;
  background: radial-gradient(ellipse at center, rgba(109,74,255,0.08), transparent 70%);
  pointer-events: none;
}
.hero .badge {
  display: inline-flex; align-items: center; gap: 8px;
  background: var(--surface); border: 1px solid var(--border2);
  padding: 6px 16px; border-radius: 100px;
  font-size: 0.8rem; color: var(--text-dim);
  margin-bottom: 32px; position: relative;
  box-shadow: var(--shadow-sm);
}
.hero .badge .dot { width: 8px; height: 8px; background: var(--success); border-radius: 50%; }
.hero h1 {
  font-size: clamp(2.5rem, 6vw, 4.5rem);
  font-weight: 800; letter-spacing: -0.03em;
  line-height: 1.05; max-width: 800px;
  margin-bottom: 24px; position: relative;
  color: var(--text);
}
.hero h1 .gradient {
  background: linear-gradient(135deg, var(--accent), var(--complement));
  -webkit-background-clip: text; -webkit-text-fill-color: transparent;
  background-clip: text;
}
.hero p.sub {
  font-size: 1.25rem; color: var(--text-dim);
  max-width: 600px; margin-bottom: 40px; position: relative;
}
.hero .cta-group { display: flex; gap: 16px; flex-wrap: wrap; justify-content: center; position: relative; }
.btn {
  display: inline-flex; align-items: center; gap: 10px;
  padding: 14px 28px; border-radius: 12px;
  font-weight: 600; font-size: 1rem;
  transition: transform .15s, box-shadow .15s, background .2s, border-color .2s;
  cursor: pointer; border: none;
  font-family: var(--sans);
}
.btn-primary {
  background: var(--accent); color: #fff;
  box-shadow: 0 4px 20px rgba(109,74,255,0.3);
}
.btn-primary:hover { text-decoration: none; transform: translateY(-2px); box-shadow: 0 8px 30px rgba(109,74,255,0.4); }
.btn-secondary {
  background: var(--surface); color: var(--text);
  border: 1px solid var(--border2);
  box-shadow: var(--shadow-sm);
}
.btn-secondary:hover { text-decoration: none; transform: translateY(-2px); border-color: var(--accent); box-shadow: var(--shadow); }

/* Terminal preview */
.terminal {
  margin-top: 60px; max-width: 720px; width: 100%;
  background: #1a1a2e; border: 1px solid #2a2a3e;
  border-radius: 12px; overflow: hidden;
  box-shadow: var(--shadow-lg);
  position: relative;
  text-align: left;
}
.terminal-bar {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 16px; background: #12121a;
  border-bottom: 1px solid #2a2a3e;
}
.terminal-bar .dot-r { width: 12px; height: 12px; border-radius: 50%; background: #ff5f57; }
.terminal-bar .dot-y { width: 12px; height: 12px; border-radius: 50%; background: #febc2e; }
.terminal-bar .dot-g { width: 12px; height: 12px; border-radius: 50%; background: #28c840; }
.terminal-bar .title { margin-left: 12px; font-size: 0.8rem; color: #8888aa; font-family: var(--mono); }
.terminal-body {
  padding: 20px; font-family: var(--mono); font-size: 0.85rem;
  overflow-x: auto; line-height: 1.7; color: #e4e4ef;
}
.terminal-body .cmd { color: #00d4aa; }
.terminal-body .out { color: #8888aa; }
.terminal-body .key { color: #7c9fff; }
.terminal-body .str { color: #ff6b9d; }
.terminal-body .comment { color: #555566; }

/* Sections */
section { padding: 100px 24px; max-width: 1100px; margin: 0 auto; }
.section-label {
  font-size: 0.8rem; font-weight: 600; text-transform: uppercase;
  letter-spacing: 0.1em; color: var(--complement-deep); margin-bottom: 12px;
}
.section-title {
  font-size: clamp(1.8rem, 4vw, 2.8rem);
  font-weight: 700; letter-spacing: -0.02em;
  margin-bottom: 16px; max-width: 700px;
  color: var(--text);
}
.section-desc { font-size: 1.1rem; color: var(--text-dim); max-width: 600px; margin-bottom: 48px; }
section code {
  font-family: var(--mono); font-size: 0.9em; background: var(--surface2);
  padding: 2px 6px; border-radius: 4px; color: var(--accent-deep);
}

/* Feature grid */
.features { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 24px; }
.feature {
  background: var(--bg); border: 1px solid var(--border);
  border-radius: 16px; padding: 32px;
  transition: border-color .2s, transform .2s, box-shadow .2s;
  box-shadow: var(--shadow-sm);
}
.feature:hover { border-color: var(--accent); transform: translateY(-4px); box-shadow: var(--shadow); }
.feature .icon { font-size: 1.8rem; margin-bottom: 16px; }
.feature h3 { font-size: 1.2rem; font-weight: 700; margin-bottom: 8px; color: var(--text); }
.feature p { color: var(--text-dim); font-size: 0.95rem; }
.feature p code { font-size: 0.85em; }

/* Why section */
.why-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 48px; align-items: start; }
@media (max-width: 768px) { .why-grid { grid-template-columns: 1fr; } }
.why-card {
  background: var(--surface); border: 1px solid var(--border2);
  border-radius: 12px; padding: 24px; margin-bottom: 16px;
}
.why-card .label { font-family: var(--mono); font-size: 0.8rem; color: var(--complement-deep); margin-bottom: 8px; font-weight: 600; letter-spacing: 0.05em; }
.why-card .desc { font-size: 0.95rem; color: var(--text-dim); }
.why-card .desc code { font-size: 0.9em; }

/* Code block */
.code-block {
  background: #1a1a2e; border: 1px solid #2a2a3e;
  border-radius: 12px; padding: 24px;
  font-family: var(--mono); font-size: 0.85rem;
  overflow-x: auto; line-height: 1.7; color: #e4e4ef;
  text-align: left;
}
.code-block .cmd { color: #00d4aa; }
.code-block .key { color: #7c9fff; }
.code-block .str { color: #ff6b9d; }
.code-block .comment { color: #555566; }
.code-block .out { color: #8888aa; }

/* Exit codes table */
.exit-table { width: 100%; border-collapse: collapse; margin-top: 24px; font-size: 0.9rem; background: var(--bg); border-radius: 12px; overflow: hidden; box-shadow: var(--shadow-sm); }
.exit-table th, .exit-table td { text-align: left; padding: 12px 16px; border-bottom: 1px solid var(--border); }
.exit-table th { color: var(--text-dim); font-weight: 600; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.05em; background: var(--surface); }
.exit-table td { font-family: var(--mono); color: var(--text); }
.exit-table td:first-child { color: var(--accent); font-weight: 700; }
.exit-table td:nth-child(2) { color: var(--complement-deep); font-weight: 600; }
.exit-table tr:last-child td { border-bottom: none; }
.exit-table tr:hover { background: var(--surface); }

/* Install section */
.install-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 20px; margin-top: 32px; }
.install-card {
  background: var(--surface); border: 1px solid var(--border2);
  border-radius: 12px; padding: 24px;
}
.install-card h4 { font-size: 0.85rem; font-weight: 600; color: var(--complement-deep); margin-bottom: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
.install-card code {
  display: block; font-family: var(--mono); font-size: 0.85rem;
  background: var(--bg); padding: 12px; border-radius: 8px;
  border: 1px solid var(--border); color: var(--accent-deep);
  word-break: break-all; white-space: pre-wrap;
}

/* CTA section */
.cta-section {
  text-align: center; padding: 120px 24px;
  background: linear-gradient(180deg, var(--bg) 0%, var(--bg2) 100%);
  border-top: 1px solid var(--border);
}
.cta-section h2 {
  font-size: clamp(2rem, 5vw, 3.5rem);
  font-weight: 800; letter-spacing: -0.02em;
  margin-bottom: 16px; color: var(--text);
}
.cta-section h2 .gradient {
  background: linear-gradient(135deg, var(--accent), var(--complement));
  -webkit-background-clip: text; -webkit-text-fill-color: transparent;
  background-clip: text;
}
.cta-section p { color: var(--text-dim); font-size: 1.15rem; margin-bottom: 40px; }

/* Footer */
footer {
  border-top: 1px solid var(--border);
  padding: 40px 24px; text-align: center;
  color: var(--text-dim); font-size: 0.85rem;
  background: var(--bg);
}
footer a { color: var(--accent-deep); }
footer .disclaimer { margin-top: 16px; font-size: 0.75rem; max-width: 600px; margin-left: auto; margin-right: auto; line-height: 1.5; color: var(--text-faint); }

/* Responsive */
@media (max-width: 640px) {
  nav ul { display: none; }
  nav { justify-content: space-between; }
  .hero { padding-top: 100px; }
}

/* Animations */
@keyframes fadeUp {
  from { opacity: 0; transform: translateY(20px); }
  to { opacity: 1; transform: translateY(0); }
}
.fade-up { animation: fadeUp 0.6s ease-out; }
.fade-up-delay-1 { animation: fadeUp 0.6s ease-out 0.1s both; }
.fade-up-delay-2 { animation: fadeUp 0.6s ease-out 0.2s both; }
.fade-up-delay-3 { animation: fadeUp 0.6s ease-out 0.3s both; }
</style>
</head>
<body>

<nav>
  <div class="logo">higgs<span>.</span></div>
  <ul>
    <li><a href="#why">Why</a></li>
    <li><a href="#features">Features</a></li>
    <li><a href="#contract">Agent Contract</a></li>
    <li><a href="#install">Install</a></li>
    <li><a href="https://github.com/akeemjenkins/higgs">GitHub</a></li>
  </ul>
  <a class="nav-cta" href="#install">Get Started</a>
</nav>

<!-- Hero -->
<div class="hero">
  <div class="badge fade-up"><span class="dot"></span> v1.0.3 released — cosign-signed, SBOM-included</div>
  <h1 class="fade-up fade-up-delay-1">The CLI your AI agent <span class="gradient">drives</span> to manage your inbox</h1>
  <p class="sub fade-up fade-up-delay-2">higgs is an agent-first CLI for Proton Mail. Schema manifest, NDJSON streaming, typed error envelopes, and stable exit codes — designed for a language model to drive, not a human to parse.</p>
  <div class="cta-group fade-up fade-up-delay-3">
    <a class="btn btn-primary" href="#install">⚡ Install</a>
    <a class="btn btn-secondary" href="https://github.com/akeemjenkins/higgs" target="_blank">View on GitHub →</a>
  </div>

  <div class="terminal fade-up fade-up-delay-3">
    <div class="terminal-bar">
      <div class="dot-r"></div><div class="dot-y"></div><div class="dot-g"></div>
      <div class="title">higgs classify --dry-run --limit 3 INBOX</div>
    </div>
    <div class="terminal-body">
<span class="comment"># The agent discovers the tool first</span>
<span class="cmd">$</span> higgs schema classify
<span class="out">{ "name": "classify", "stdout": "ndjson", "exit_codes": [0,2,3,4,5,6,7,9] }</span>

<span class="comment"># Then drives it — dry run, NDJSON on stdout</span>
<span class="cmd">$</span> higgs classify --dry-run --limit 3 INBOX
<span class="out">{"mailbox":"INBOX","uid":1842,"subject":"Your order has shipped","suggested_labels":["Orders"],"confidence":0.94}</span>
<span class="out">{"mailbox":"INBOX","uid":1843,"subject":"Stripe receipt","suggested_labels":["Finance"],"confidence":0.91}</span>
<span class="out">{"mailbox":"INBOX","uid":1844,"subject":"GitHub PR review","suggested_labels":["Work"],"confidence":0.97}</span>
<span class="out">{"type":"summary","mailbox":"INBOX","classified":3,"errors":0,"skipped":0}</span>

<span class="comment"># Every stream ends with a terminator. No heuristics needed.</span>
    </div>
  </div>
</div>

<!-- Why -->
<section id="why">
  <div class="section-label">Why higgs exists</div>
  <h2 class="section-title">Wiring a CLI into an agent loop shouldn't be painful</h2>
  <p class="section-desc">Most CLIs mix prose and data on stdout, errors are English sentences, exit codes are 0-or-1, and the only tool spec is <code>--help</code>. higgs inverts every one of those assumptions.</p>

  <div class="why-grid">
    <div>
      <div class="why-card">
        <div class="label">THE PROBLEM</div>
        <div class="desc">Agents parse <code>--help</code> text, guess at flags, and try to interpret unstructured stdout. Errors are English sentences they can't reliably branch on. Exit codes are binary. Streams have no end signal.</div>
      </div>
      <div class="why-card">
        <div class="label">THE OLD WORKAROUND</div>
        <div class="desc">Prompt-engineer the agent with examples of every command. Parse stdout with regex or JSON extraction. Retry blindly on failure. Hope the output format doesn't change.</div>
      </div>
    </div>
    <div>
      <div class="why-card">
        <div class="label">THE HIGGS WAY</div>
        <div class="desc">Load <code>higgs schema</code> once — a JSON manifest of every subcommand, its flags, args, stdout format, and exit codes. Drive the CLI from that spec. Branch on <code>.error.kind</code>. Detect stream completion via the <code>{"type":"summary"}</code> terminator.</div>
      </div>
      <div class="why-card">
        <div class="label">THE RESULT</div>
        <div class="desc">An agent that can discover, drive, and recover from errors in higgs without any prompt engineering. The schema IS the prompt. The error envelope IS the branching logic. The exit code IS the retry strategy.</div>
      </div>
    </div>
  </div>
</section>

<!-- Features -->
<section id="features">
  <div class="section-label">What's inside</div>
  <h2 class="section-title">Built for agents, useful for humans</h2>
  <p class="section-desc">The first workload is a local-only Proton Mail inbox classifier. The contract is the real product.</p>

  <div class="features">
    <div class="feature">
      <div class="icon">📐</div>
      <h3>Schema Manifest</h3>
      <p><code>higgs schema</code> emits a JSON description of every subcommand — flags, args, stdout format, exit codes. An agent loads it once and can drive the tool without prompt-engineered syntax.</p>
    </div>
    <div class="feature">
      <div class="icon">📦</div>
      <h3>NDJSON Streaming</h3>
      <p>Every streaming command emits one JSON object per line and ends with a <code>{"type":"summary"}</code> terminator. Callers know when a stream is done — no heuristics, no timeouts.</p>
    </div>
    <div class="feature">
      <div class="icon">🛡️</div>
      <h3>Typed Error Envelopes</h3>
      <p>Every failure emits <code>{"error":{"kind","code","reason","message","hint"}}</code>. Agents branch on <code>.error.kind</code>, not on parsed English. Retry on 5, prompt the user on 2, surface on 4.</p>
    </div>
    <div class="feature">
      <div class="icon">🔒</div>
      <h3>Local-Only by Design</h3>
      <p>No API keys, no cloud inference, no telemetry. Mail flows through Proton Bridge on localhost. Classification runs through a local Ollama model. Nothing leaves your machine.</p>
    </div>
    <div class="feature">
      <div class="icon">🔑</div>
      <h3>Secrets Out-of-Band</h3>
      <p>Credentials go to the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux) with an AES-256-GCM file fallback. Nothing sensitive flows through an agent's context window.</p>
    </div>
    <div class="feature">
      <div class="icon">💾</div>
      <h3>Checkpointed State</h3>
      <p>SQLite state DB with <code>backfill</code> and <code>state clear</code>. Runs are resumable across crashes and restarts — the agent picks up where it left off.</p>
    </div>
  </div>
</section>

<!-- Agent Contract -->
<section id="contract">
  <div class="section-label">The agent contract</div>
  <h2 class="section-title">Four primitives. Zero ambiguity.</h2>
  <p class="section-desc">Everything an agent needs to discover, drive, and recover from higgs — built into the CLI itself.</p>

  <div class="code-block">
<span class="comment">// 1. Discover: load the schema manifest</span>
<span class="cmd">$</span> higgs schema classify
<span class="out">{</span>
  <span class="key">"name"</span>: <span class="str">"classify"</span>,
  <span class="key">"summary"</span>: <span class="str">"Classify messages with Ollama and optionally apply labels"</span>,
  <span class="key">"args"</span>: [{"name":"mailbox","required":false,"default":"INBOX"}],
  <span class="key">"flags"</span>: [
    {"name":"dry-run","type":"bool","description":"Preview without writing labels"},
    {"name":"apply","type":"bool","description":"Apply suggested labels to IMAP"},
    {"name":"limit","type":"int","default":100},
    {"name":"workers","type":"int","default":4}
  ],
  <span class="key">"stdout"</span>: <span class="str">"ndjson"</span>,
  <span class="key">"exit_codes"</span>: [0,2,3,4,5,6,7,9]
<span class="out">}</span>

<span class="comment">// 2. Drive: NDJSON on stdout, human progress on stderr</span>
<span class="cmd">$</span> higgs classify --dry-run --limit 3 INBOX
<span class="out">{"mailbox":"INBOX","uid":1842,"subject":"Order shipped","suggested_labels":["Orders"],"confidence":0.94}</span>
<span class="out">{"type":"summary","mailbox":"INBOX","classified":3,"errors":0,"skipped":0}</span>

<span class="comment">// 3. Recover: typed error envelopes, not English sentences</span>
<span class="cmd">$</span> higgs classify --apply INBOX
<span class="out">{</span>
  <span class="key">"error"</span>: {
    <span class="key">"kind"</span>: <span class="str">"auth"</span>,
    <span class="key">"code"</span>: <span class="str">401"</span>,
    <span class="key">"reason"</span>: <span class="str">"authFailed"</span>,
    <span class="key">"message"</span>: <span class="str">"IMAP authentication failed"</span>,
    <span class="key">"hint"</span>: <span class="str">"Check PM_IMAP_PASSWORD matches your Bridge credentials"</span>
  }
<span class="out">}</span>
<span class="comment">// exit code: 2 → agent prompts user for new credentials</span>

<span class="comment">// 4. Branch: exit codes map 1:1 to error kinds</span>
  </div>

  <table class="exit-table">
    <thead><tr><th>Code</th><th>Kind</th><th>Agent Strategy</th></tr></thead>
    <tbody>
      <tr><td>0</td><td>success</td><td>Parse stdout, continue</td></tr>
      <tr><td>2</td><td>auth</td><td>Prompt user for credentials</td></tr>
      <tr><td>3</td><td>validation</td><td>Fix flags/args, retry</td></tr>
      <tr><td>4</td><td>config</td><td>Surface missing config to caller</td></tr>
      <tr><td>5</td><td>imap</td><td>Retry with backoff</td></tr>
      <tr><td>6</td><td>classify</td><td>Check Ollama, retry</td></tr>
      <tr><td>9</td><td>internal</td><td>Escalate to user</td></tr>
    </tbody>
  </table>
</section>

<!-- Install -->
<section id="install">
  <div class="section-label">Get started</div>
  <h2 class="section-title">Install in 60 seconds</h2>
  <p class="section-desc">Download a release binary, install via Go, or build from source. Then set Bridge + Ollama env vars and run.</p>

  <div class="install-grid">
    <div class="install-card">
      <h4>macOS / Linux (tarball)</h4>
      <code>curl -L https://github.com/akeemjenkins/higgs/releases/latest/download/higgs_1.0.3_darwin_arm64.tar.gz | tar xz</code>
    </div>
    <div class="install-card">
      <h4>Go install</h4>
      <code>go install github.com/akeemjenkins/higgs/cmd/higgs@latest</code>
    </div>
    <div class="install-card">
      <h4>Build from source</h4>
      <code>git clone https://github.com/akeemjenkins/higgs.git
cd higgs && make build</code>
    </div>
  </div>

  <div style="margin-top: 40px;">
    <p style="color: var(--text-dim); margin-bottom: 16px;">Then set up your environment and dry-run:</p>
    <div class="code-block">
<span class="comment"># Prerequisites: Proton Mail Bridge + Ollama running</span>
<span class="cmd">$</span> ollama pull gemma4

<span class="comment"># Set Bridge credentials</span>
<span class="cmd">$</span> export PM_IMAP_USERNAME=<span class="str">"alice@proton.me"</span>
<span class="cmd">$</span> export PM_IMAP_PASSWORD=<span class="str">"bridge...word"</span>
<span class="cmd">$</span> export PM_IMAP_HOST=<span class="str">"127.0.0.1"</span>
<span class="cmd">$</span> export PM_IMAP_PORT=<span class="str">"1143"</span>
<span class="cmd">$</span> export PM_OLLAMA_MODEL=<span class="str">"gemma4"</span>

<span class="comment"># Dry-run — preview classifications without writing labels</span>
<span class="cmd">$</span> higgs classify --dry-run --limit 20 INBOX

<span class="comment"># When the suggestions look right, apply them</span>
<span class="cmd">$</span> higgs classify --apply --workers 4 INBOX
    </div>
  </div>

  <div style="text-align: center; margin-top: 48px;">
    <a class="btn btn-primary" href="https://github.com/akeemjenkins/higgs/releases/latest" target="_blank">⬇ Download v1.0.3</a>
    <a class="btn btn-secondary" href="https://github.com/akeemjenkins/higgs" target="_blank" style="margin-left: 12px;">Read the docs →</a>
  </div>
</section>

<!-- CTA -->
<div class="cta-section">
  <h2>Give your agent a CLI it can <span class="gradient">actually drive</span></h2>
  <p>Schema-discoverable. NDJSON-native. Zero telemetry. Local-only.</p>
  <div style="display: flex; gap: 16px; justify-content: center; flex-wrap: wrap;">
    <a class="btn btn-primary" href="#install">Install higgs</a>
    <a class="btn btn-secondary" href="https://github.com/akeemjenkins/higgs" target="_blank">Star on GitHub</a>
  </div>
</div>

<!-- Footer -->
<footer>
  <div>
    <strong style="color: var(--accent-deep);">higgs</strong> — agent-first CLI for Proton Mail ·
    <a href="https://github.com/akeemjenkins/higgs">GitHub</a> ·
    <a href="https://github.com/akeemjenkins/higgs/releases">Releases</a> ·
    Apache 2.0 License
  </div>
  <div class="disclaimer">
    Unofficial project. higgs is an independent, community-built CLI for Proton Mail. It is not affiliated with, endorsed by, or sponsored by Proton AG. "Proton", "Proton Mail", and related marks are trademarks of Proton AG; this project uses them only to describe interoperability.
  </div>
  <div style="margin-top: 12px;">Built by Akeem Jenkins</div>
</footer>

</body>
</html>`;
