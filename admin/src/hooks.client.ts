import type { HandleClientError } from '@sveltejs/kit/hooks';

// The API's error text is meant for the admin, so it is shown as is.
export const handleError: HandleClientError = ({ error }) => ({
	message: error instanceof Error ? error.message : 'Unknown error'
});
