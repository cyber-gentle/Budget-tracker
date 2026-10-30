/**
 * Tailwind config for Spendly's compiled stylesheet
 * (internal/app/templates/css/tailwind.css). Rebuild after changing any
 * template, script or this file:  ./scripts/build-css.sh
 *
 * Merged from the per-page configs the pages used with the Tailwind Play CDN.
 */
module.exports = {
  content: [
    './internal/app/templates/**/*.{html,js}',
    './internal/app/**/*.go',
  ],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'sans-serif'],
        heading: ['"Plus Jakarta Sans"', 'Inter', 'sans-serif'],
      },
      colors: {
        offwhite: '#F8FAFC',
        brand: { 50: '#F0FDF4', 500: '#10B981', 600: '#059669', 900: '#064E3B' },
      },
    },
  },
};
