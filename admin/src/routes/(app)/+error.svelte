<script lang="ts">
	import { page } from '$app/state';
</script>

<p class="text-sm text-destructive">Failed to load: {page.error?.message ?? 'unknown error'}</p>
