import { useEffect } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router';
import { RoomPage } from '@/room/RoomPage';
import { applyTheme, usePrefs } from '@/state/prefs';
import { TooltipProvider } from '@/ui/Tooltip';

/**
 * One room, one page. The site root *is* the door: a viewer or host link is
 * just `/` with a key in the query, and anything else lands there too (old
 * `/r/<id>` links included, which now simply meet the password prompt).
 */
export function App() {
  const theme = usePrefs((s) => s.theme);
  useEffect(() => applyTheme(theme), [theme]);
  return (
    <TooltipProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<RoomPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </TooltipProvider>
  );
}
