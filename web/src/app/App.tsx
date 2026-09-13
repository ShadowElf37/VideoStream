import { useEffect } from 'react';
import { BrowserRouter, Link, Route, Routes } from 'react-router';
import { RoomPage } from '@/room/RoomPage';
import { applyTheme, usePrefs } from '@/state/prefs';
import { TooltipProvider } from '@/ui/Tooltip';
import { Landing } from './Landing';

export function App() {
  const theme = usePrefs((s) => s.theme);
  useEffect(() => applyTheme(theme), [theme]);
  return (
    <TooltipProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Landing />} />
          <Route path="/r/:id" element={<RoomPage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </BrowserRouter>
    </TooltipProvider>
  );
}

function NotFound() {
  return (
    <div className="min-h-full flex items-center justify-center p-6">
      <div className="text-center">
        <h1 className="text-2xl font-semibold">Nothing here</h1>
        <p className="text-muted mt-1">
          <Link to="/" className="text-accent underline">
            Back to the start
          </Link>
        </p>
      </div>
    </div>
  );
}
