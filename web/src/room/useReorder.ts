import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Drag-to-reorder for the Up next list.
 *
 * Hand-rolled on pointer events rather than pulling in a drag-and-drop
 * library: the list is a handful of rows in a sidebar, and the whole of what
 * is needed is "which row is the pointer over" — a bounding-rect comparison
 * against the rows already in the DOM. HTML5 drag-and-drop is not an option
 * either; it does not fire on touch.
 *
 * The order shown is optimistic. The server owns the queue and broadcasts it
 * back, so the draft is held only until the answer arrives, and dropped
 * outright if the answer is an error — a list that silently disagrees with the
 * room is worse than one that snaps back.
 */

/** moveItem is the whole model: take the item at `from`, put it at `to`. */
export function moveItem<T>(items: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || from >= items.length) return items;
  const target = Math.min(Math.max(to, 0), items.length - 1);
  if (target === from) return items;
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(target, 0, item as T);
  return next;
}

export function sameOrder(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

export interface Reorder {
  /** What to render: the draft while one is in flight, else the room's order. */
  order: string[];
  /** The row currently being dragged, for styling. */
  dragging: number | null;
  listRef: React.RefObject<HTMLOListElement | null>;
  /** Props for the grab handle of row `index`. */
  handleProps(index: number): {
    onPointerDown: (e: React.PointerEvent) => void;
    onPointerMove: (e: React.PointerEvent) => void;
    onPointerUp: (e: React.PointerEvent) => void;
    onPointerCancel: (e: React.PointerEvent) => void;
    onKeyDown: (e: React.KeyboardEvent) => void;
  };
  /** Optimistically drop a row, e.g. for the × button. */
  remove(index: number, commit: () => Promise<void>): void;
}

export function useReorder(ids: string[], commit: (next: string[]) => Promise<void>): Reorder {
  const listRef = useRef<HTMLOListElement>(null);
  const [draft, setDraft] = useState<string[] | null>(null);
  const [dragging, setDragging] = useState<number | null>(null);
  const order = draft ?? ids;

  // Let go of the draft once the room agrees with it, so later broadcasts are
  // rendered rather than shadowed by a stale optimistic order forever.
  useEffect(() => {
    if (draft && sameOrder(draft, ids)) setDraft(null);
  }, [ids, draft]);

  const send = useCallback(
    (next: string[], run: () => Promise<void>) => {
      setDraft(next);
      void run().catch(() => setDraft(null));
    },
    [],
  );

  const indexAt = useCallback((clientY: number): number | null => {
    const el = listRef.current;
    if (!el) return null;
    const rows = Array.from(el.children) as HTMLElement[];
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i]!.getBoundingClientRect();
      if (clientY < r.top + r.height / 2) return i;
    }
    return rows.length - 1;
  }, []);

  const finish = useCallback(
    (next: string[] | null) => {
      setDragging(null);
      if (!next || sameOrder(next, ids)) {
        setDraft(null);
        return;
      }
      void commit(next).catch(() => setDraft(null));
    },
    [commit, ids],
  );

  const handleProps = useCallback(
    (index: number) => ({
      onPointerDown: (e: React.PointerEvent) => {
        if (e.button !== 0 && e.pointerType === 'mouse') return;
        e.preventDefault();
        e.currentTarget.setPointerCapture(e.pointerId);
        setDragging(index);
        setDraft(order);
      },
      onPointerMove: (e: React.PointerEvent) => {
        if (dragging === null) return;
        const to = indexAt(e.clientY);
        if (to === null || to === dragging) return;
        setDraft((d) => moveItem(d ?? ids, dragging, to));
        setDragging(to);
      },
      onPointerUp: (e: React.PointerEvent) => {
        if (dragging === null) return;
        e.currentTarget.releasePointerCapture(e.pointerId);
        finish(draft);
      },
      onPointerCancel: () => {
        setDragging(null);
        setDraft(null);
      },
      onKeyDown: (e: React.KeyboardEvent) => {
        // Alt, because the bare arrows move focus and the sidebar scrolls.
        if (!e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
        const to = index + (e.key === 'ArrowUp' ? -1 : 1);
        const next = moveItem(order, index, to);
        if (next === order) return;
        e.preventDefault();
        send(next, () => commit(next));
      },
    }),
    [order, dragging, draft, ids, indexAt, finish, send, commit],
  );

  const remove = useCallback(
    (index: number, run: () => Promise<void>) => {
      send(order.filter((_, i) => i !== index), run);
    },
    [order, send],
  );

  return { order, dragging, listRef, handleProps, remove };
}
