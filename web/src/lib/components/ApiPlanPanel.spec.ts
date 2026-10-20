import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ApiPlanPanel from './ApiPlanPanel.svelte';

vi.mock('$app/paths', () => ({ resolve: (p: string) => p, base: '', assets: '' }));
vi.mock('$lib/i18n/currentLocale.svelte', () => ({ locale: () => 'en' }));
vi.mock('$lib/auth.svelte', () => ({ isAuthenticated: () => true }));
const { getAPIPlan } = vi.hoisted(() => ({ getAPIPlan: vi.fn() }));
vi.mock('$lib/api', () => ({ api: { getAPIPlan } }));

describe('ApiPlanPanel', () => {
  it('names the tier and its rate for a subscriber', async () => {
    getAPIPlan.mockResolvedValue({ tier: 'api', per_minute: 1200, until: '2026-11-19T00:00:00Z' });
    render(ApiPlanPanel);
    expect(await screen.findByText(/1,200/)).toBeTruthy();
    expect(screen.getByText('API')).toBeTruthy();
  });

  it('points an account without the product to the plans', async () => {
    getAPIPlan.mockResolvedValue({ tier: '', per_minute: 0 });
    render(ApiPlanPanel);
    const link = await screen.findByRole('link', { name: 'See API plans' });
    expect(link.getAttribute('href')).toBe('/pricing#api');
  });
});
