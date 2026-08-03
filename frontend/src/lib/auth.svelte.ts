import { api, type Me, type User } from '$lib/api';

// Global auth state (Svelte 5 runes module).
export const auth = $state<{ me: Me | null; loaded: boolean }>({
	me: null,
	loaded: false
});

export async function loadUser(): Promise<Me | null> {
	try {
		auth.me = await api.get<Me>('/api/auth/me');
	} catch {
		auth.me = null;
	}
	auth.loaded = true;
	return auth.me;
}

export async function logout() {
	try {
		await api.post('/api/auth/logout');
	} finally {
		auth.me = null;
	}
}
