// Shared date/time formatting helpers.

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

export function weekdayName(weekday: number): string {
	return WEEKDAYS[weekday] ?? '?';
}

/** "2026-06-08" → "Mon 08.06." */
export function formatDate(date: string): string {
	const d = new Date(date + 'T00:00:00');
	const wd = WEEKDAYS[(d.getDay() + 6) % 7];
	const day = String(d.getDate()).padStart(2, '0');
	const month = String(d.getMonth() + 1).padStart(2, '0');
	return `${wd} ${day}.${month}.`;
}

/** "19:00" + 90 → "19:00–20:30" */
export function formatTimeRange(time: string, durationMinutes: number): string {
	const [h, m] = time.split(':').map(Number);
	const end = h * 60 + m + durationMinutes;
	const eh = Math.floor(end / 60) % 24;
	const em = end % 60;
	return `${time}–${String(eh).padStart(2, '0')}:${String(em).padStart(2, '0')}`;
}

/** SQLite "2026-06-06 14:03:22" (UTC) or ISO string → local "06.06. 16:03" */
export function formatTimestamp(ts: string): string {
	if (!ts) return '';
	const iso = ts.includes('T') ? ts : ts.replace(' ', 'T') + 'Z';
	const d = new Date(iso);
	if (isNaN(d.getTime())) return ts;
	const day = String(d.getDate()).padStart(2, '0');
	const month = String(d.getMonth() + 1).padStart(2, '0');
	const hh = String(d.getHours()).padStart(2, '0');
	const mm = String(d.getMinutes()).padStart(2, '0');
	return `${day}.${month}. ${hh}:${mm}`;
}
