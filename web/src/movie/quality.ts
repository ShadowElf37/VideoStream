import { create } from 'zustand';
import { usePrefs } from '@/state/prefs';

/**
 * Which rendition this viewer is watching.
 *
 * The control that claimed to offer a quality choice was removed because it
 * selected a simulcast layer nobody published. This one is real: the server
 * publishes an HLS master playlist with a variant per rendition, and the
 * player can move between them mid-film without the <video> element being
 * touched — which is the only reason a manual picker is worth having rather
 * than a second copy of the film behind a page reload.
 *
 * "Auto" is the default and is honest about what it does: the player measures
 * throughput and picks. The named entries are for the case a measurement
 * cannot help with — knowing you are about to be on a train.
 */

export interface Level {
  /** The player's own index, which is what selecting one needs. */
  index: number;
  /** "1080p", "720p" — derived from the height, so it matches the rendition. */
  name: string;
  height: number;
  kbps: number;
}

/** What a menu entry is worth remembering as. */
export const AUTO = 'auto';

interface QualityStore {
  /** Empty when nothing switchable is playing: no menu at all, then. */
  levels: Level[];
  /** The preference, by name: AUTO or a level name. */
  selected: string;
  /** What the player is actually showing right now, -1 before it decides. */
  active: number;
  /** Installed by whatever is driving the element; a no-op otherwise. */
  apply: (name: string) => void;
  set(levels: Level[], apply: (name: string) => void): void;
  select(name: string): void;
  setActive(index: number): void;
  clear(): void;
}

export const useQuality = create<QualityStore>()((set, get) => ({
  levels: [],
  selected: AUTO,
  active: -1,
  apply: () => undefined,
  set: (levels, apply) => {
    set({ levels, apply, active: -1 });
    // Re-apply whatever was remembered: a new film is not a reason to lose
    // the choice made about this connection.
    apply(get().selected);
  },
  select: (name) => {
    set({ selected: name });
    // Remembered per device: the reason to override Auto is usually the
    // connection, and the connection outlives the film.
    usePrefs.getState().set('movieQuality', name);
    get().apply(name);
  },
  setActive: (index) => set({ active: index }),
  clear: () => set({ levels: [], active: -1, apply: () => undefined }),
}));

/** levelIndex maps a remembered name onto the player's own numbering. */
export function levelIndex(levels: Level[], name: string): number {
  if (name === AUTO) return -1;
  const found = levels.find((l) => l.name === name);
  return found ? found.index : -1;
}

/** levelName is what a height is called, matching vspush's rendition names. */
export function levelName(height: number, index: number): string {
  return height > 0 ? `${height}p` : `level ${index + 1}`;
}

/**
 * The label for the menu button: the choice, plus what it actually resolved to
 * when that is not obvious. "Auto" alone is the one thing a viewer cannot
 * check for themselves.
 */
export function qualityLabel(selected: string, levels: Level[], active: number): string {
  if (selected !== AUTO) return selected;
  const cur = levels.find((l) => l.index === active);
  return cur ? `Auto · ${cur.name}` : 'Auto';
}
