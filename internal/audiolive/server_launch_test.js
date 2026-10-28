const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");

// The Go test supplies the actual private file emitted by companion.launchFile.
const html = fs.readFileSync(process.argv[2], "utf8");
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/gi)];
assert.equal(scripts.length, 1, "launch page must contain one inline script");
assert.doesNotMatch(html, /\b(?:src|href|action)\s*=|http-equiv\s*=/i);
const staticPage = html.replace(scripts[0][0], "");
assert.match(staticPage, /current Chrome or Edge/);
assert.doesNotMatch(staticPage, /synthetic-local-test-token|0123456789abcdef/);

const expected = "http://0123456789abcdef0123456789abcdef.localhost:12345/#synthetic-local-test-token";
const desktop = (changes = {}) => ({
  mobile: false,
  platform: "Windows",
  brands: [{ brand: "Not A Brand", version: "99" }, { brand: "Chromium", version: "141" }],
  ...changes,
});
const fixtures = [
  ...["Windows", "macOS", "Linux", "Chrome OS"].map(platform => ({
    name: "desktop " + platform, agent: desktop({ platform }), accepted: true,
  })),
  { name: "minimum version", agent: desktop({ brands: [{ brand: "Chromium", version: "100" }] }), accepted: true },
  { name: "Chrome brands", agent: desktop({ brands: [{ brand: "Google Chrome", version: "141" }, { brand: "Chromium", version: "141" }] }), accepted: true },
  { name: "Edge brands", agent: desktop({ brands: [{ brand: "Microsoft Edge", version: "141" }, { brand: "Chromium", version: "141" }] }), accepted: true },
  { name: "no client hints" },
  { name: "null client hints", agent: null },
  { name: "throwing client hints", throwingAgent: true },
  { name: "Safari", userAgent: "Mozilla/5.0 (Macintosh) Version/26.0 Safari/605.1.15" },
  { name: "Safari Chrome emulation", userAgent: "Mozilla/5.0 (Macintosh) Chrome/141.0.0.0 Safari/537.36" },
  { name: "legacy Edge", userAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/70.0.3538.102 Safari/537.36 Edge/18.19045" },
  { name: "iOS Chrome", userAgent: "Mozilla/5.0 (iPhone) CriOS/141.0.0.0 Mobile Safari/604.1" },
  { name: "iOS Edge", userAgent: "Mozilla/5.0 (iPad) EdgiOS/141.0.0.0 Mobile Safari/604.1" },
  { name: "insecure", agent: desktop(), secure: false },
  { name: "missing secure context", agent: desktop(), secure: undefined },
  { name: "mobile", agent: desktop({ mobile: true }) },
  { name: "missing mobile", agent: desktop({ mobile: undefined }) },
  { name: "string mobile", agent: desktop({ mobile: "false" }) },
  ...["Android", "iOS", "iPadOS", "Unknown", "", undefined].map(platform => ({
    name: "unsupported platform " + platform, agent: desktop({ platform }),
  })),
  ...[undefined, null, {}, "Chromium", [], [{ brand: "Google Chrome", version: "141" }]].map((brands, index) => ({
    name: "unsupported brands " + index, agent: desktop({ brands }),
  })),
  ...["99", "099", "100.0", "1e2", "Infinity", "100foo", "1000", "", 100, undefined].map(version => ({
    name: "unsupported version " + version,
    agent: desktop({ brands: [{ brand: "Chromium", version }] }),
  })),
  { name: "wrong brand case", agent: desktop({ brands: [{ brand: "chromium", version: "141" }] }) },
  { name: "throwing brands", agent: Object.defineProperty(desktop(), "brands", { get() { throw new Error("synthetic metadata failure"); } }) },
  { name: "null brand entry", agent: desktop({ brands: [null] }) },
  { name: "throwing version", agent: desktop({ brands: [{ brand: "Chromium", get version() { throw new Error("synthetic version failure"); } }] }) },
];

for (const fixture of fixtures) {
  const navigation = [];
  const network = [];
  const navigator = { userAgent: fixture.userAgent || "synthetic desktop browser" };
  Object.defineProperty(navigator, "userAgentData", {
    get() {
      if (fixture.throwingAgent) throw new Error("synthetic client hints failure");
      return fixture.agent;
    },
  });
  const location = {
    replace(target) { navigation.push(["replace", target]); },
    assign(target) { navigation.push(["assign", target]); },
    set href(target) { navigation.push(["href", target]); },
  };
  const context = {
    navigator, location,
    isSecureContext: Object.prototype.hasOwnProperty.call(fixture, "secure") ? fixture.secure : true,
    fetch() { network.push("fetch"); },
    XMLHttpRequest: function () { network.push("XMLHttpRequest"); },
    WebSocket: function () { network.push("WebSocket"); },
    Image: function () { network.push("Image"); },
  };
  vm.runInNewContext(scripts[0][1], context, { timeout: 100 });
  assert.deepEqual(network, [], fixture.name + " must not probe a network destination");
  assert.deepEqual(navigation, fixture.accepted ? [["replace", expected]] : [], fixture.name);
}
console.log("Private launch browser gate passed " + fixtures.length + " fixtures.");
