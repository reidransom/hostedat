<script lang="ts">
	import { workers } from '$api/client';
	import type { WorkerEnvVar, KVNamespace, CronSchedule, WorkerLog, D1Database, DurableObjectNamespace } from '$api/types';
	import EnvVars from './EnvVars.svelte';
	import KVPanel from './KVPanel.svelte';
	import D1Panel from './D1Panel.svelte';
	import DurableObjects from './DurableObjects.svelte';
	import CronPanel from './CronPanel.svelte';
	import LogViewer from './LogViewer.svelte';
	import { Variable, Database, HardDrive, Timer, ScrollText, Boxes } from 'lucide-svelte';
	import { onMount } from 'svelte';

	interface Props {
		siteId: string;
		hasWorker: boolean;
	}

	let { siteId, hasWorker }: Props = $props();

	let activeTab = $state('env');

	const tabs = [
		{ id: 'env', label: 'Env Vars', icon: Variable },
		{ id: 'kv', label: 'KV', icon: Database },
		{ id: 'd1', label: 'D1', icon: HardDrive },
		{ id: 'do', label: 'Durable Objects', icon: Boxes },
		{ id: 'crons', label: 'Crons', icon: Timer },
		{ id: 'logs', label: 'Logs', icon: ScrollText }
	];

	const visibleTabs = $derived(hasWorker ? tabs : tabs.filter((tab) => tab.id === 'env'));

	let envVars = $state<WorkerEnvVar[]>([]);
	let kvNamespaces = $state<KVNamespace[]>([]);
	let d1Databases = $state<D1Database[]>([]);
	let doNamespaces = $state<DurableObjectNamespace[]>([]);
	let cronSchedules = $state<CronSchedule[]>([]);
	let logs = $state<WorkerLog[]>([]);

	async function loadEnv() { envVars = await workers.listEnv(siteId); }
	async function loadKV() { kvNamespaces = await workers.listKV(siteId); }
	async function loadD1() { d1Databases = (await workers.listD1(siteId)).items; }
	async function loadDO() { doNamespaces = (await workers.listDurableObjects(siteId)).items; }
	async function loadCrons() { cronSchedules = await workers.listCrons(siteId); }
	async function loadLogs() { logs = await workers.getLogs(siteId); }

	onMount(() => {
		loadEnv();
	});

	function handleTabChange(id: string) {
		activeTab = id;
		if (id === 'env') loadEnv();
		else if (id === 'kv') loadKV();
		else if (id === 'd1') loadD1();
		else if (id === 'do') loadDO();
		else if (id === 'crons') loadCrons();
		else if (id === 'logs') loadLogs();
	}
</script>

{#if !hasWorker}
	<p class="mb-4 text-sm text-text-muted">Configure environment variables and secrets before your first deployment. Other worker bindings and logs are available after deploying a worker script.</p>
{/if}
	<div class="flex gap-6">
		<!-- Vertical tab list -->
		<nav class="w-40 shrink-0 space-y-1">
			{#each visibleTabs as tab}
				{@const Icon = tab.icon}
				<button
					onclick={() => handleTabChange(tab.id)}
					class="flex items-center gap-2 w-full rounded-lg px-3 py-2 text-sm text-left transition-colors
						{activeTab === tab.id
							? 'bg-primary/10 text-primary'
							: 'text-text-muted hover:text-text hover:bg-elevated'}"
				>
					<Icon class="size-4 shrink-0" />
					{tab.label}
				</button>
			{/each}
		</nav>

		<!-- Content area -->
		<div class="flex-1 min-w-0">
			{#if activeTab === 'env'}
				<EnvVars {siteId} items={envVars} onRefresh={loadEnv} />
			{:else if hasWorker && activeTab === 'kv'}
				<KVPanel {siteId} items={kvNamespaces} onRefresh={loadKV} />
			{:else if hasWorker && activeTab === 'd1'}
				<D1Panel {siteId} items={d1Databases} onRefresh={loadD1} />
			{:else if hasWorker && activeTab === 'do'}
				<DurableObjects {siteId} items={doNamespaces} onRefresh={loadDO} />
			{:else if hasWorker && activeTab === 'crons'}
				<CronPanel {siteId} items={cronSchedules} onRefresh={loadCrons} />
			{:else if hasWorker && activeTab === 'logs'}
				<LogViewer items={logs} onRefresh={loadLogs} />
			{/if}
		</div>
	</div>
