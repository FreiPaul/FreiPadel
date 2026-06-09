<script lang="ts">
	import { toggleMode } from 'mode-watcher';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import { Button } from '$lib/components/ui/button';
</script>

<Button
	variant="ghost"
	size="icon"
	onclick={toggleMode}
	title="Toggle light / dark theme"
	aria-label="Toggle theme"
>
	<SunIcon class="size-5 dark:hidden" />
	<MoonIcon class="hidden size-5 dark:block" />
</Button>
