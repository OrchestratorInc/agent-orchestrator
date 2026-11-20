// Tailwind compiles the utilities used by the docs' own components
// (src/components); fumadocs ships its own as precompiled CSS.
export default {
  plugins: {
    "@tailwindcss/postcss": {},
  },
};
