<script lang="ts">
	import '../app.css';
	import favicon from '$lib/assets/favicon.svg';
	import { Toaster } from '$lib/components/ui/sonner';
	import { ModeWatcher } from 'mode-watcher';

	let { children } = $props();
</script>

<svelte:head>
	<title>FreiPadel</title>
	<link rel="icon" href={favicon} />
</svelte:head>

<ModeWatcher />
<Toaster richColors />

{@render children()}
