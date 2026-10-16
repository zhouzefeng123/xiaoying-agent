// Sized up from the original 720x768 chat column: the app now opens at
// EXPANDED_WINDOW_WIDTH + APP_SIDEBAR_WIDTH (1120x940), and the widest layout
// (chat + sidebar + coder panel) still fits a 1707px work area.
export const COLLAPSED_WINDOW_WIDTH = 720;
export const EXPANDED_WINDOW_WIDTH = 840;
export const EXPANDED_WINDOW_HEIGHT = 940;
export const COLLAPSED_WINDOW_HEIGHT = 190;
export const APP_SIDEBAR_WIDTH = 280;
export const CODER_PANEL_WIDTH = 800;
export const WORD_PANEL_WIDTH = 700;

export const EXPANDED_WINDOW_WIDTH_WITH_PANEL = Math.round(EXPANDED_WINDOW_WIDTH * 0.75);
