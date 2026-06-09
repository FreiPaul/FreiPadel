import { api, type User } from '$lib/api';

// Global auth state (Svelte 5 runes module).
export const auth = $state<{ user: User | null; loaded: boolean }>({
	user: null,
	loaded: false
});

export async function loadUser(): Promise<User | null> {
	try {
		auth.user = await api.get<User>('/api/auth/me');
	} catch {
		auth.user = null;
	}
	auth.loaded = true;
	return auth.user;
}

export async function logout() {
	try {
		await api.post('/api/auth/logout');
	} finally {
		auth.user = null;
	}
}
