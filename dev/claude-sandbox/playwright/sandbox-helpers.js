// Claude-sandbox tooling, not part of the shipped app. See ../README.md.
//
// Reusable Playwright helpers for driving the real server in this sandbox,
// where outbound access to youtube.com and cdn.jsdelivr.net is blocked but
// the npm registry is reachable. Vendor the two CDN assets the base
// templates load once per container (see ../README.md for the exact `npm
// pack` commands), then:
//
//   const { newPlayerContext, login, confirmYes } = require("./sandbox-helpers");
//   const ctx = await newPlayerContext(browser, {
//     htmxJs: fs.readFileSync("/tmp/htmx-pkg/package/dist/htmx.js", "utf8"),
//     biCss: fs.readFileSync("/tmp/bi-pkg/package/font/bootstrap-icons.min.css", "utf8"),
//     biWoff2: fs.readFileSync("/tmp/bi-pkg/package/font/fonts/bootstrap-icons.woff2"),
//     biWoff: fs.readFileSync("/tmp/bi-pkg/package/font/fonts/bootstrap-icons.woff"),
//   });
//   await login(ctx, { name: "Alice", password: "PlaytestPass123!" });
//   const page = await ctx.newPage();
//   await page.goto(base + "/track-timeline/" + lobbyId, { waitUntil: "networkidle" });
//
// Check the framework's base.html (gameshell-framework, not this repo) if
// pages stop loading -- the exact CDN URLs/versions routed here are only
// current as of when this file was written; a framework upgrade could bump
// them.

const fs = require("fs");
const path = require("path");

const YT_STUB = fs.readFileSync(path.join(__dirname, "yt-stub.js"), "utf8");

// newPlayerContext creates a fresh, isolated browser context (its own cookie
// jar -- one per player) with the YouTube IFrame API always stubbed and, if
// asset buffers are passed, the htmx/bootstrap-icons CDN requests answered
// from the local vendor copies instead of hitting the blocked real CDN.
async function newPlayerContext(browser, assets = {}, contextOptions = {}) {
  const ctx = await browser.newContext({ viewport: { width: 1300, height: 950 }, ...contextOptions });

  await ctx.route("**/iframe_api", (route) =>
    route.fulfill({ contentType: "application/javascript", body: YT_STUB })
  );

  if (assets.htmxJs) {
    await ctx.route("https://cdn.jsdelivr.net/npm/htmx.org@2.0.7/dist/htmx.js", (route) =>
      route.fulfill({ contentType: "application/javascript", body: assets.htmxJs })
    );
  }
  if (assets.biCss) {
    await ctx.route(
      "https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css",
      (route) => route.fulfill({ contentType: "text/css", body: assets.biCss })
    );
  }
  if (assets.biWoff2) {
    await ctx.route(
      "https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/fonts/bootstrap-icons.woff2*",
      (route) => route.fulfill({ contentType: "font/woff2", body: assets.biWoff2 })
    );
  }
  if (assets.biWoff) {
    await ctx.route(
      "https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/fonts/bootstrap-icons.woff*",
      (route) => route.fulfill({ contentType: "font/woff", body: assets.biWoff })
    );
  }

  return ctx;
}

// login authenticates a browser context against the real /api/user/login
// endpoint (form: name, password) -- the session cookie lands in the
// context's own cookie jar, so every page opened from it is that user.
async function login(context, user, baseUrl = "http://127.0.0.1:2016") {
  const resp = await context.request.post(baseUrl + "/api/user/login", {
    form: { name: user.name, password: user.password },
  });
  if (resp.status() !== 200) {
    throw new Error("login failed for " + user.name + ": " + (await resp.text()));
  }
}

// confirmYes answers this app's custom in-page <dialog id="confirmation-dialog">
// (overrides htmx's native hx-confirm via the htmx:confirm event -- see
// global.js). Playwright's page.on("dialog", ...) does NOT fire for this,
// since it's not a real browser-native confirm().
async function confirmYes(page) {
  await page.waitForSelector("#confirmation-dialog[open]", { timeout: 5000 });
  await page.click('#confirmation-dialog button:has-text("Yes")');
}

module.exports = { newPlayerContext, login, confirmYes };
