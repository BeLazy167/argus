import { SignIn } from "@clerk/nextjs";

export default function SignInPage() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-void">
      <SignIn
        appearance={{
          elements: {
            rootBox: "mx-auto",
            card: "bg-charcoal border border-iron",
          },
        }}
      />
    </div>
  );
}
