import { defineConfig, devices } from '@playwright/test'

/**
 * The browser half of the verification matrix in ARCHITECTURE.md.
 *
 * Everything below the browser is tested hard already, against a real Postgres.
 * What has never been tested is the part only a browser can run: that the SPA
 * actually walks a guest from a search to a confirmed booking, that the `<head>`
 * the Go server writes reaches the document a crawler would index, and that the
 * booking flow is not crawlable. Those are the properties here, and nothing that
 * a Go test could assert more cheaply is repeated in them.
 *
 * The server under test is the real binary, run by `go run` rather than from
 * bin/: `go build -o bin/bealhouse` writes a file with no extension, which a
 * Windows shell will not execute, and a stale bin/bealhouse.exe beside it is a
 * documented way to lose an afternoon (CLAUDE.md). `go run` has neither problem
 * and is the same code.
 *
 * It runs against the development database and books real rooms, in a window of
 * the calendar nothing else uses — see e2e/support/window.ts.
 */
export default defineConfig({
  testDir: './e2e',

  // The suite books rooms, and two workers booking the same room in the same
  // window is a race this suite would report as a bug in the application.
  // Serial, and the tests are seconds each.
  workers: 1,
  fullyParallel: false,

  // A retry re-books, which is only safe because each test claims its own
  // nights. One, because a flake worth seeing twice is worth seeing, and a
  // green-on-the-third-go suite is one nobody trusts.
  retries: process.env.CI ? 1 : 0,

  reporter: process.env.CI ? [['github'], ['list']] : [['list']],

  // A committed booking left behind takes a room off sale in the e2e window for
  // good, so the next run finds nothing available. Both hooks run the same
  // cleanup: setup because the last run may have been killed partway, teardown
  // because this one should not leave the same mess.
  globalSetup: './e2e/support/clean.ts',
  globalTeardown: './e2e/support/clean.ts',

  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8099',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },

  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],

  webServer: {
    // From web/, so the module root is one up.
    command: 'go run ./cmd/server',
    cwd: '..',
    url: 'http://127.0.0.1:8099/api/health',
    reuseExistingServer: !process.env.CI,

    // `go run` compiles first, and a cold build cache is a minute of it.
    timeout: 180_000,

    stdout: 'pipe',
    stderr: 'pipe',

    env: {
      ADDR: ':8099',

      // A real database, because everything this suite asserts — availability,
      // the exclusion constraint behind a hold, the head's live prices — is a
      // property of the data.
      DATABASE_URL:
        process.env.DATABASE_URL ??
        'postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable',

      // The stand-in processor, which is what makes the journey walkable
      // without an account. Everything past its button is real: a properly
      // signed delivery through the same webhook handler, signature
      // verification and state machine a live payment would use. It refuses to
      // exist unless ENV is dev and no Stripe variable is set at all, so this
      // block must not grow one.
      ENV: 'dev',
      STRIPE_FAKE: 'true',

      // The origin the server builds canonical, og: and sitemap URLs from, and
      // the one the head tests assert against. Absolute URLs simply do not
      // appear without it.
      SITE_URL: 'http://127.0.0.1:8099',

      // Not a secret here and deliberately fixed: the manage link only appears
      // in the confirmation when there is one, and a test asserting a
      // confirmation is complete should be looking at the shape a guest gets.
      BOOKING_LINK_SECRET: 'e2e-not-a-secret',

      // Uploads are not exercised here, but the server logs loudly when it has
      // nowhere to put one. Its own directory rather than the repository's.
      MEDIA_DIR: 'web/e2e/.media',
    },
  },
})
