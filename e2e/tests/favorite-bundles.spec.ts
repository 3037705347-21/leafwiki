import { APIRequestContext, expect, test } from '@playwright/test';
import { createPageViaApi, csrfHeader, loggedIn } from '../helpers/apiContext';
import LoginPage from '../pages/LoginPage';
import ViewPage from '../pages/ViewPage';

const viewerPassword = 'viewer-password-123';

async function createViewer(admin: APIRequestContext, username: string): Promise<string> {
  const resp = await admin.post('/api/users', {
    headers: await csrfHeader(admin),
    data: {
      username,
      email: `${username}@example.com`,
      password: viewerPassword,
      role: 'viewer',
    },
  });
  expect(resp.status(), `create user ${username}`).toBe(201);
  return (await resp.json()).id;
}

async function favoritePage(ctx: APIRequestContext, pageId: string) {
  const resp = await ctx.put(`/api/pages/${pageId}/favorite`, {
    headers: await csrfHeader(ctx),
  });
  expect(resp.status(), `favorite ${pageId}`).toBe(200);
}

async function favoriteIds(ctx: APIRequestContext): Promise<string[]> {
  const resp = await ctx.get('/api/favorites');
  expect(resp.status()).toBe(200);
  const body = (await resp.json()) as { pages: Array<{ id: string }> };
  return body.pages.map((p) => p.id);
}

async function createBundle(
  ctx: APIRequestContext,
  name: string,
  pageIds: string[],
): Promise<{ id: string; status: number; body: unknown }> {
  const resp = await ctx.post('/api/favorite-bundles', {
    headers: await csrfHeader(ctx),
    data: { name, description: '', pageIds },
  });
  const body = (await resp.json().catch(() => null)) as unknown;
  const id = body && typeof body === 'object' && 'id' in body ? (body as { id: string }).id : '';
  return { id, status: resp.status(), body };
}

test.describe('favorite bundles (team reading lists)', () => {
  test('admin validation and atomic sync over the API', async () => {
    const admin = await loggedIn();
    const ts = Date.now();
    const p1 = await createPageViaApi(admin, {
      title: `Bundle One ${ts}`,
      slug: `bundle-one-${ts}`,
      content: '# One\n',
    });
    const p2 = await createPageViaApi(admin, {
      title: `Bundle Two ${ts}`,
      slug: `bundle-two-${ts}`,
      content: '# Two\n',
    });
    const p3 = await createPageViaApi(admin, {
      title: `Bundle Three ${ts}`,
      slug: `bundle-three-${ts}`,
      content: '# Three\n',
    });

    // A missing page rejects the whole save with 400.
    const missing = await createBundle(admin, `missing-${ts}`, [p1, 'no-such-page']);
    expect(missing.status).toBe(400);
    expect((missing.body as { error: { code: string } }).error.code).toBe(
      'favorite_bundle_entry_page_missing',
    );

    // A duplicate entry rejects the whole save with 409.
    const dup = await createBundle(admin, `dup-${ts}`, [p1, p1]);
    expect(dup.status).toBe(409);
    expect((dup.body as { error: { code: string } }).error.code).toBe(
      'favorite_bundle_duplicate_entry',
    );

    const valid = await createBundle(admin, `team-${ts}`, [p1, p2]);
    expect(valid.status).toBe(201);

    const username = `bundleviewer-${ts}`;
    const viewerId = await createViewer(admin, username);
    const viewer = await loggedIn(username, viewerPassword);

    // Non-admins cannot manage lists.
    const forbidden = await viewer.post('/api/favorite-bundles', {
      headers: await csrfHeader(viewer),
      data: { name: 'sneaky', pageIds: [] },
    });
    expect(forbidden.status()).toBe(403);

    // First sync subscribes and favorites both pages.
    const firstSync = await viewer.post(`/api/favorite-bundles/${valid.id}/sync`, {
      headers: await csrfHeader(viewer),
    });
    expect(firstSync.status()).toBe(200);
    expect(await favoriteIds(viewer)).toEqual(expect.arrayContaining([p1, p2]));

    // A personal favorite on a new bundle entry conflicts: the whole sync is
    // rejected (409) and the existing favorites are untouched.
    await favoritePage(viewer, p3);
    const updated = await admin.put(`/api/favorite-bundles/${valid.id}`, {
      headers: await csrfHeader(admin),
      data: { name: `team-${ts}`, description: '', pageIds: [p1, p2, p3] },
    });
    expect(updated.status()).toBe(200);

    const conflict = await viewer.post(`/api/favorite-bundles/${valid.id}/sync`, {
      headers: await csrfHeader(viewer),
    });
    expect(conflict.status()).toBe(409);
    expect((await conflict.json()).error.code).toBe('favorite_bundle_personal_conflict');
    expect(await favoriteIds(viewer)).toEqual(expect.arrayContaining([p1, p2, p3]));

    // Once the list no longer contains the conflicting page, sync succeeds
    // again and reconciles (drops p2 from the list, keeps personal p3).
    const fixed = await admin.put(`/api/favorite-bundles/${valid.id}`, {
      headers: await csrfHeader(admin),
      data: { name: `team-${ts}`, description: '', pageIds: [p1] },
    });
    expect(fixed.status()).toBe(200);

    const resync = await viewer.post(`/api/favorite-bundles/${valid.id}/sync`, {
      headers: await csrfHeader(viewer),
    });
    expect(resync.status()).toBe(200);
    const result = (await resync.json()) as { added: string[]; removed: string[] };
    expect(result.added).toEqual([]);
    expect(result.removed).toEqual([p2]);
    expect(await favoriteIds(viewer)).toEqual(expect.arrayContaining([p1, p3]));

    // Unsubscribing releases the list-owned favorite but not the personal one.
    const unsub = await viewer.post(`/api/favorite-bundles/${valid.id}/unsubscribe`, {
      headers: await csrfHeader(viewer),
    });
    expect(unsub.status()).toBe(204);
    expect(await favoriteIds(viewer)).toEqual([p3]);

    // Unsubscribe is idempotent.
    const again = await viewer.post(`/api/favorite-bundles/${valid.id}/unsubscribe`, {
      headers: await csrfHeader(viewer),
    });
    expect(again.status()).toBe(204);

    await viewer.dispose();
    await admin.delete(`/api/users/${viewerId}`, {
      headers: await csrfHeader(admin),
    });
    await admin.delete(`/api/favorite-bundles/${valid.id}`, {
      headers: await csrfHeader(admin),
    });
    await admin.dispose();
  });

  test('a user subscribes and reconciles a reading list from the sidebar', async ({ page }) => {
    const admin = await loggedIn();
    const ts = Date.now();
    const p1 = await createPageViaApi(admin, {
      title: `Sidebar List Alpha ${ts}`,
      slug: `sb-list-alpha-${ts}`,
      content: '# Alpha\n',
    });
    const p2 = await createPageViaApi(admin, {
      title: `Sidebar List Beta ${ts}`,
      slug: `sb-list-beta-${ts}`,
      content: '# Beta\n',
    });
    const p3 = await createPageViaApi(admin, {
      title: `Sidebar Personal ${ts}`,
      slug: `sb-list-personal-${ts}`,
      content: '# Personal\n',
    });
    const bundle = await createBundle(admin, `sidebar-team-${ts}`, [p1, p2]);

    // The viewer personally favorites p3 before ever opening the UI, so the
    // favorites list loads with it after login.
    const username = `sidebar-viewer-${ts}`;
    const viewerId = await createViewer(admin, username);
    const viewer = await loggedIn(username, viewerPassword);
    await favoritePage(viewer, p3);
    await viewer.dispose();

    // Admin shortens the list (drops p2) before the viewer's first sync —
    // the first sync should reconcile straight to the current content.
    const shortened = await admin.put(`/api/favorite-bundles/${bundle.id}`, {
      headers: await csrfHeader(admin),
      data: {
        name: `sidebar-team-${ts}`,
        description: '',
        pageIds: [p1],
      },
    });
    expect(shortened.status()).toBe(200);

    const loginPage = new LoginPage(page);
    const viewPage = new ViewPage(page);
    await loginPage.goto();
    await loginPage.login(username, viewerPassword);
    await viewPage.expectUserLoggedIn();

    // Expand the reading-lists sidebar section and subscribe. Scope every
    // row action to this test's bundle name — other specs' lists may share
    // the same server instance.
    const bundleName = `sidebar-team-${ts}`;
    const bundleRow = page
      .getByTestId('bundles-section')
      .locator('[data-testid^="bundle-row-"]', { hasText: bundleName });
    await page.getByRole('button', { name: 'Reading lists', exact: true }).click();
    await expect(page.getByTestId('bundles-section')).toBeVisible();
    await bundleRow.locator('[data-testid^="bundle-sync-"]').click();
    await expect(page.locator('[data-sonner-toast][data-type="success"]')).toBeVisible();

    // Favorites now contain the list page p1 and the personal p3, but never
    // p2 (the list dropped it before the first sync). Scope to the favorites
    // section because the same titles also appear as tree nodes.
    const favoritesSection = page.getByTestId('favorites-section');
    await expect(favoritesSection.getByTestId('favorite-item')).toHaveCount(2);
    await expect(favoritesSection.getByText(`Sidebar List Alpha ${ts}`)).toBeVisible();
    await expect(favoritesSection.getByText(`Sidebar Personal ${ts}`)).toBeVisible();
    await expect(favoritesSection.getByText(`Sidebar List Beta ${ts}`)).toHaveCount(0);

    // Unsubscribe keeps the personal favorite and drops the list one.
    await bundleRow.locator('[data-testid^="bundle-unsubscribe-"]').click();
    await expect(favoritesSection.getByTestId('favorite-item')).toHaveCount(1);
    await expect(favoritesSection.getByText(`Sidebar Personal ${ts}`)).toBeVisible();

    await admin.delete(`/api/users/${viewerId}`, {
      headers: await csrfHeader(admin),
    });
    await admin.delete(`/api/favorite-bundles/${bundle.id}`, {
      headers: await csrfHeader(admin),
    });
    await admin.dispose();
  });
});
