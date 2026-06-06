import nextCoreWebVitals from 'eslint-config-next/core-web-vitals';

export default [
  ...nextCoreWebVitals,
  {
    rules: {
      'react-hooks/error-boundaries': 'off',
      'react-hooks/immutability': 'off',
      'react-hooks/purity': 'off',
      'react-hooks/set-state-in-effect': 'off',
      'react-hooks/static-components': 'off',
    },
  },
  {
    ignores: [
      '.next/**',
      'next-env.d.ts',
      'node_modules/**',
    ],
  },
];
