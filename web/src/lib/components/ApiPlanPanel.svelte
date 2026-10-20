<script lang="ts">
  import { resolve } from '$app/paths';
  import { api } from '$lib/api';
  import { AsyncData } from '$lib/asyncData.svelte';
  import { isAuthenticated } from '$lib/auth.svelte';
  import { locale } from '$lib/i18n/currentLocale.svelte';
  import { t } from '$lib/i18n/t';
  import type { APIPlan } from '$lib/types';
  import { messages } from './ApiPlanPanel.messages';

  // Which API product tier the account holds, beside the keys it applies to. Only full-access
  // keys get the plan's rate; an embed key is published on other sites by design.
  const s = $derived(t(messages, locale()));

  const plan = new AsyncData<APIPlan | null>(null);
  $effect(() => {
    if (isAuthenticated()) void plan.run(() => api.getAPIPlan());
  });

  const until = (iso: string) =>
    new Date(iso).toLocaleDateString(locale(), { year: 'numeric', month: 'long', day: 'numeric' });
</script>

{#if plan.value}
  <section class="flex flex-col gap-2 rounded-xl border border-border p-4">
    <h2 class="text-base font-semibold">{s.title}</h2>
    {#if plan.value.tier}
      <p class="text-sm text-muted-foreground">
        <span class="font-medium text-foreground">{s.tiers[plan.value.tier]}</span>:
        {plan.value.per_minute.toLocaleString(locale())}
        {s.active}
        {plan.value.until ? until(plan.value.until) : ''}.
      </p>
    {:else}
      <p class="text-sm text-muted-foreground">{s.none}</p>
      <!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() is applied; the rule cannot see through the appended section anchor -->
      <a href={resolve('/pricing') + '#api'} class="text-sm font-medium underline-offset-4 hover:underline"
        >{s.seePlans}</a
      >
    {/if}
  </section>
{/if}
