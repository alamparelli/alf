import { Platform } from 'react-native';

// ALF Design System — Refined Native Dark
// Inspired by iOS native apps: iMessage, Linear, Arc

export const colors = {
  // Backgrounds (OLED-friendly)
  bg: '#000000',
  surface: '#1C1C1E',
  surfaceElevated: '#2C2C2E',
  surfaceHover: '#3A3A3C',

  // Accent — iOS system blue, trustworthy and clean
  accent: '#0A84FF',
  accentSoft: 'rgba(10, 132, 255, 0.12)',
  accentMuted: 'rgba(10, 132, 255, 0.25)',

  // Text hierarchy
  textPrimary: '#FFFFFF',
  textSecondary: '#8E8E93',
  textTertiary: '#636366',

  // Semantic
  destructive: '#FF453A',
  destructiveSoft: 'rgba(255, 69, 58, 0.12)',
  success: '#30D158',
  warning: '#FF9F0A',

  // Borders & dividers
  separator: '#38383A',
  border: '#48484A',

  // Message bubbles
  userBubble: '#0A84FF',
  assistantBubble: '#1C1C1E',

  // Overlays
  overlay: 'rgba(0, 0, 0, 0.6)',
  overlayLight: 'rgba(0, 0, 0, 0.4)',
} as const;

export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 20,
  xxl: 32,
  xxxl: 48,
} as const;

export const radius = {
  sm: 8,
  md: 12,
  lg: 16,
  xl: 20,
  full: 999,
} as const;

export const typography = {
  // System font for native feel
  largeTitle: {
    fontSize: 34,
    fontWeight: '700' as const,
    letterSpacing: 0.37,
    color: colors.textPrimary,
  },
  title: {
    fontSize: 22,
    fontWeight: '700' as const,
    letterSpacing: 0.35,
    color: colors.textPrimary,
  },
  headline: {
    fontSize: 17,
    fontWeight: '600' as const,
    letterSpacing: -0.41,
    color: colors.textPrimary,
  },
  body: {
    fontSize: 17,
    fontWeight: '400' as const,
    letterSpacing: -0.41,
    color: colors.textPrimary,
  },
  callout: {
    fontSize: 16,
    fontWeight: '400' as const,
    letterSpacing: -0.32,
    color: colors.textPrimary,
  },
  subhead: {
    fontSize: 15,
    fontWeight: '400' as const,
    letterSpacing: -0.24,
    color: colors.textSecondary,
  },
  footnote: {
    fontSize: 13,
    fontWeight: '400' as const,
    letterSpacing: -0.08,
    color: colors.textSecondary,
  },
  caption: {
    fontSize: 12,
    fontWeight: '400' as const,
    color: colors.textTertiary,
  },
  mono: {
    fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace',
    fontSize: 14,
  },
} as const;
