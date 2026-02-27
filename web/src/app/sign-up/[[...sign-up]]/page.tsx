import { SignUp } from "@clerk/nextjs";

export default function SignUpPage() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-void">
      <SignUp
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
