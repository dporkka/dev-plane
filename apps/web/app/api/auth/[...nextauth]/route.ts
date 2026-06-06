import { NextRequest, NextResponse } from 'next/server';

export async function GET(req: NextRequest) {
  // This is a placeholder for NextAuth.js or custom auth handling
  // The actual GitHub OAuth is handled by the Go backend
  return NextResponse.json({ message: 'Auth endpoint' });
}

export async function POST(req: NextRequest) {
  return NextResponse.json({ message: 'Auth endpoint' });
}
