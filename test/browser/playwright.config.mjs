import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.WDE_BROWSER_BASE_URL ?? 'http://127.0.0.1:8082';

export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.mjs',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  outputDir: 'artifacts/test-results',
  reporter: [
    ['list'],
    ['html', { outputFolder: 'artifacts/html-report', open: 'never' }],
    ['junit', { outputFile: 'artifacts/results.xml' }],
  ],
  use: {
    baseURL,
    locale: 'pt-BR',
    colorScheme: 'dark',
    // A login failure could otherwise capture the password input as pixels.
    // Successful evidence is attached explicitly only after navigation to /app.
    screenshot: 'off',
    trace: 'off',
    video: 'off',
  },
  projects: [
    {
      name: 'desktop-chromium',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      name: 'mobile-chromium',
      use: { ...devices['Pixel 7'] },
    },
  ],
});
