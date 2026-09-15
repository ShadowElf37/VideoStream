const ua = typeof navigator !== 'undefined' ? navigator.userAgent : '';

export const isSafari = /Safari/.test(ua) && !/Chrome|Chromium|CriOS|Edg|OPR|Firefox|FxiOS/.test(ua);
export const isFirefox = /Firefox|FxiOS/.test(ua);
export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

/** `setSinkId` on media elements (speaker selection). Missing on Safari. */
export const supportsSinkId =
  typeof HTMLMediaElement !== 'undefined' && 'setSinkId' in HTMLMediaElement.prototype;

/** `jitterBufferTarget` on RTCRtpReceiver (Chrome 108+, Firefox 122+). Missing on Safari. */
export const supportsJitterBufferTarget =
  typeof RTCRtpReceiver !== 'undefined' &&
  ('jitterBufferTarget' in RTCRtpReceiver.prototype || 'playoutDelayHint' in RTCRtpReceiver.prototype);

export const prefersReducedMotion =
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

export function isTypingTarget(el: EventTarget | null): boolean {
  if (typeof HTMLElement === 'undefined' || !(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable;
}
