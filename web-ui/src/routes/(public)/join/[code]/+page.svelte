<!--
  /join/<code>: the page of an invite (Add a computer). lib/public/CodePage.
-->
<script lang="ts">
	import { page } from '$app/state';
	import CodePage from '#lib/public/CodePage.svelte';
</script>

<CodePage code={page.params.code ?? ''} kind="join" />
