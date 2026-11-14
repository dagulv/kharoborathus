import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true,
				experimental: { async: true }
			},
			// shadcn-svelte generates $lib imports, which kit 3 only resolves through an alias.
			alias: { $lib: 'src/lib' },
			// Served by bunny.net, which rewrites unknown paths to the fallback.
			adapter: adapter({ fallback: 'index.html' })
		})
	]
});
