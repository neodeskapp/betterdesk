<!--
  /s/<code>: the page of a quick support link. lib/public/CodePage.
-->
<script lang="ts">
	import { page } from '$app/state';
	import CodePage from '#lib/public/CodePage.svelte';
</script>

<CodePage code={page.params.code ?? ''} kind="support" />
